package application

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/h2non/filetype"

	"github.com/buger/jsonparser"
	"github.com/pkg/errors"
	"go2tv.app/go2tv/v2/castprotocol/v2/cast"
	pb "go2tv.app/go2tv/v2/castprotocol/v2/cast/proto"
	"go2tv.app/go2tv/v2/castprotocol/v2/playlists"
	"go2tv.app/go2tv/v2/castprotocol/v2/storage"
	"path/filepath"
)

var (
	// Global request id
	requestID int
	_         App = &Application{}
)

const (
	// 'CC1AD845' seems to be a predefined app; check link
	// https://gist.github.com/jloutsenhizer/8855258
	defaultChromecastAppID = "CC1AD845"

	defaultSender = "sender-0"
	defaultRecv   = "receiver-0"

	namespaceConn  = "urn:x-cast:com.google.cast.tp.connection"
	namespaceRecv  = "urn:x-cast:com.google.cast.receiver"
	namespaceMedia = "urn:x-cast:com.google.cast.media"
)

type PlayedItem struct {
	ContentID string `json:"content_id"`
	Started   int64  `json:"started"`
	Finished  int64  `json:"finished"`
}

type CastMessageFunc func(*pb.CastMessage)

type App interface {
	SetConn(conn cast.Conn)
	SetIface(*net.Interface)
	SetDebug(bool)
	SetCacheDisabled(bool)
	SetConnectionRetries(int)
	SetServerPort(int)
	Start(addr string, port int) error
	Close(stopMedia bool) error
	LoadApp(appID, contentID string) error
	Status() (*cast.Application, *cast.Media, *cast.Volume)
	Info() (*cast.DeviceInfo, error)
	Update() error
	Pause() error
	Unpause() error
	TogglePause() error
	Stop() error
	StopMedia() error
	Seek(value int) error
	SeekFromStart(value int) error
	SeekToTime(value float32) error
	Skipad() error
	Load(filenameOrUrl string, startTime int, contentType string, transcode, detach, forceDetach bool) error
	QueueLoad(filenames []string, contentType string, transcode bool) error
	Transcode(contentType string, command string, args ...string) error
	Next() error
	Previous() error
	SetVolume(value float32) error
	SetMuted(value bool) error
	Slideshow(filenames []string, duration int, repeat bool) error
	AddMessageFunc(f CastMessageFunc)
	PlayedItems() map[string]PlayedItem
	PlayableMediaType(filename string) bool
}

type Application struct {
	conn  cast.Conn
	debug bool

	// Device name override (originating e.g. from mdns lookup).
	deviceNameOverride string

	// Internal mapping of request id to result channel
	resultChanMap map[int]chan *pb.CastMessage

	messageMu sync.Mutex
	// Relay messages received so users can add custom logic to
	// events.
	messageChan   chan *pb.CastMessage
	closeChanOnce sync.Once
	// Functions that will receive messages from 'messageChan'
	messageFuncs []CastMessageFunc

	// Current values from the chromecast.
	application *cast.Application // It is possible that there is no current application, can happen for google home.
	media       *cast.Media
	info        *cast.DeviceInfo
	// There seems to be two different volumes returned from the chromecast,
	// one for the receiver and one for the playing media. It looks we update
	// the receiver volume from go-chromecast, so we should use that one. But
	// we will keep the other one around in-case we need it at some point.
	volumeMedia    *cast.Volume
	volumeReceiver *cast.Volume

	httpServer *http.ServeMux
	serverPort int
	localIP    string
	iface      *net.Interface

	// NOTE: Currently only playing one media file at a time is handled
	mediaFinished  chan bool
	mediaFilenames []string

	playedItems   map[string]PlayedItem
	cacheDisabled bool
	cache         *storage.Storage

	// Number of connection retries to try before returning
	// an error.
	connectionRetries int

	// Time between each ad skip attempt
	skipadSleep time.Duration
	// Number of times to try to skip an ad
	skipadRetries int
}

type ApplicationOption func(*Application)

func WithIface(iface *net.Interface) ApplicationOption {
	return func(a *Application) {
		a.SetIface(iface)
	}
}

func WithServerPort(port int) ApplicationOption {
	return func(a *Application) {
		a.SetServerPort(port)
	}
}

func WithDebug(debug bool) ApplicationOption {
	return func(a *Application) {
		a.SetDebug(debug)
	}
}

func WithCacheDisabled(cacheDisabled bool) ApplicationOption {
	return func(a *Application) {
		a.SetCacheDisabled(cacheDisabled)
	}
}

func WithConnection(conn cast.Conn) ApplicationOption {
	return func(a *Application) {
		a.SetConn(conn)
	}
}

func WithConnectionRetries(connectionRetries int) ApplicationOption {
	return func(a *Application) {
		a.SetConnectionRetries(connectionRetries)
	}
}

func WithSkipadSleep(sleep time.Duration) ApplicationOption {
	return func(a *Application) {
		a.SetSkipadSleep(sleep)
	}
}

func WithSkipadRetries(retries int) ApplicationOption {
	return func(a *Application) {
		a.SetSkipadRetries(retries)
	}
}

func WithDeviceNameOverride(deviceName string) ApplicationOption {
	return func(a *Application) {
		a.SetDeviceNameOverride(deviceName)
	}
}

func NewApplication(opts ...ApplicationOption) *Application {
	a := &Application{
		conn:              cast.NewConnection(),
		resultChanMap:     map[int]chan *pb.CastMessage{},
		messageChan:       make(chan *pb.CastMessage),
		playedItems:       map[string]PlayedItem{},
		cache:             storage.NewStorage(),
		connectionRetries: 5,
		skipadSleep:       2 * time.Second,
		skipadRetries:     30,
	}

	// Apply options
	for _, o := range opts {
		o(a)
	}

	// Kick off the listener for asynchronous messages received from the
	// cast connection.
	go a.recvMessages()
	// Kick off the message channel listener.
	go a.messageChanHandler()
	return a
}

func (a *Application) SetConn(conn cast.Conn)       { a.conn = conn }
func (a *Application) SetServerPort(serverPort int) { a.serverPort = serverPort }
func (a *Application) SetConnectionRetries(connectionRetries int) {
	a.connectionRetries = connectionRetries
}
func (a *Application) SetCacheDisabled(cacheDisabled bool) { a.cacheDisabled = cacheDisabled }
func (a *Application) SetIface(iface *net.Interface)       { a.iface = iface }

func (a *Application) SetSkipadSleep(sleep time.Duration) { a.skipadSleep = sleep }
func (a *Application) SetSkipadRetries(retries int)       { a.skipadRetries = retries }

func (a *Application) SetDeviceNameOverride(deviceName string) {
	a.deviceNameOverride = deviceName
}

func (a *Application) App() *cast.Application { return a.application }
func (a *Application) Media() *cast.Media     { return a.media }
func (a *Application) Volume() *cast.Volume   { return a.volumeReceiver }

func (a *Application) AddMessageFunc(f CastMessageFunc) {
	a.messageMu.Lock()
	defer a.messageMu.Unlock()

	a.messageFuncs = append(a.messageFuncs, f)
}

func (a *Application) messageChanHandler() {
	for msg := range a.messageChan {
		a.messageMu.Lock()
		for _, f := range a.messageFuncs {
			f(msg)
		}
		a.messageMu.Unlock()
	}
}

// TODO: Clean-up: these Media* methods are a hack around trying
// to use the mediaFinished channel when it hasn't been properly
// set up. This is happening because there is some instances where we don't
// have any running media ('watch' command) but yet the 'mediaFinished'
// is littered throughout the codebase. This needs a redesign now that
// we do things other than just loading and playing media files.
func (a *Application) MediaStart() {
	a.mediaFinished = make(chan bool, 1)
}

func (a *Application) MediaWait() {
	<-a.mediaFinished
	a.mediaFinished = nil
}

func (a *Application) MediaFinished() {
	if a.mediaFinished == nil {
		return
	}
	a.mediaFinished <- true
}

func (a *Application) recvMessages() {
	for msg := range a.conn.MsgChan() {
		requestID, err := jsonparser.GetInt([]byte(*msg.PayloadUtf8), "requestId")
		if err == nil {
			if resultChan, ok := a.resultChanMap[int(requestID)]; ok {
				resultChan <- msg
				// Relay the event to any user specified message funcs.
				a.messageChan <- msg
				continue
			}
		}

		messageBytes := []byte(*msg.PayloadUtf8)
		// This already gets checked in the cast.Connection.handleMessage function.
		messageType, _ := jsonparser.GetString(messageBytes, "type")
		switch messageType {
		case "LOAD_FAILED":
			a.MediaFinished()
		case "MEDIA_STATUS":
			resp := cast.MediaStatusResponse{}
			if err := json.Unmarshal(messageBytes, &resp); err == nil {
				for _, status := range resp.Status {
					// The LoadingItemId is only set when there is a playlist and there
					// is an item being loaded to play next.
					if status.IdleReason == "FINISHED" && status.LoadingItemId == 0 {
						a.MediaFinished()
					} else if status.IdleReason == "INTERRUPTED" && status.Media.ContentId == "" {
						// This can happen when we go "next" in a playlist when it
						// is playing the last track.
						a.MediaFinished()
					}
				}
			}
		case "RECEIVER_STATUS":
			// We don't care about this when the application isn't set.
			if a.application == nil {
				break
			}
			resp := cast.ReceiverStatusResponse{}
			if err := json.Unmarshal(messageBytes, &resp); err == nil {
				// Check to see if the application on the device has changed,
				// if it has it is likely not this running instance that changed
				// it because that currently isn't possible.
				for _, app := range resp.Status.Applications {
					if app.AppId != a.application.AppId {
						a.MediaFinished()
					}
					a.application = &app
				}
				a.volumeReceiver = &resp.Status.Volume
			}
		case "CLOSE":
			a.MediaFinished()
			a.application, a.media, a.volumeReceiver = nil, nil, nil
		}
		// Relay the event to any user specified message funcs.
		a.messageChan <- msg
	}
}

func (a *Application) SetDebug(debug bool) {
	a.debug = debug
	if a.conn != nil {
		a.conn.SetDebug(debug)
	}
}

func (a *Application) Start(addr string, port int) error {
	if err := a.loadPlayedItems(); err != nil {
		a.log("unable to load played items: %v", err)
	}

	if err := a.conn.Start(addr, port); err != nil {
		return err
	}
	if err := a.sendDefaultConn(&cast.ConnectHeader); err != nil {
		return errors.Wrap(err, "unable to connect to chromecast")
	}
	return errors.Wrap(a.Update(), "unable to update application")
}

func (a *Application) loadPlayedItems() error {
	if a.cacheDisabled {
		return nil
	}

	b, err := a.cache.Load("application")
	if err != nil || len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, &a.playedItems)
}

func (a *Application) writePlayedItems() error {
	if a.cacheDisabled {
		return nil
	}

	playedItemsJson, _ := json.Marshal(a.playedItems)
	return a.cache.Save("application", playedItemsJson)
}

func (a *Application) Update() error {
	var recvStatus *cast.ReceiverStatusResponse
	var err error
	// Simple retry. We need this for when the device isn't currently
	// available, but it is likely that it will come up soon. If the device
	// has switch network addresses the caller is expected to handle that situation.
	for i := 0; i < a.connectionRetries; i++ {
		recvStatus, err = a.getReceiverStatus()
		if err == nil {
			break
		}
		a.log("error getting receiver status: %v", err)
		a.log("unable to get status from device; attempt %d/5, retrying...", i+1)
		time.Sleep(time.Second * 2)
	}
	if err != nil {
		return err
	}

	if len(recvStatus.Status.Applications) > 1 {
		a.log("more than 1 connected application on the chromecast: (%d)%#v", len(recvStatus.Status.Applications), recvStatus.Status.Applications)
	}

	// TODO(vishen): Why could there be more than one application, how to handle this?
	// For now just take the last one.
	for _, app := range recvStatus.Status.Applications {
		a.application = &app
	}
	a.volumeReceiver = &recvStatus.Status.Volume

	if a.application == nil || a.application.IsIdleScreen {
		return nil
	}

	a.updateMediaStatus()

	return nil

}

func (a *Application) updateMediaStatus() error {
	a.sendMediaConn(&cast.ConnectHeader)

	mediaStatus, err := a.getMediaStatus()
	if err != nil {
		return err
	}
	for _, media := range mediaStatus.Status {
		a.media = &media
		a.volumeMedia = &media.Volume
	}
	return nil
}

func (a *Application) Close(stopMedia bool) error {
	if stopMedia {
		a.sendMediaConn(&cast.CloseHeader)
		a.sendDefaultConn(&cast.CloseHeader)
	}
	defer a.closeChanOnce.Do(func() {
		close(a.messageChan)
	})
	return a.conn.Close()
}

func (a *Application) Status() (*cast.Application, *cast.Media, *cast.Volume) {
	return a.application, a.media, a.volumeReceiver
}

func (a *Application) Info() (*cast.DeviceInfo, error) {
	if a.info != nil {
		return a.info, nil
	}
	addr, err := a.conn.RemoteAddr()
	if err != nil {
		return nil, err
	}
	info, err := GetInfo(addr)
	if err != nil {
		return nil, err
	}
	if len(a.deviceNameOverride) > 0 {
		info.Name = a.deviceNameOverride
	}
	a.info = info
	return info, err
}

func (a *Application) Pause() error {
	if a.media == nil {
		return ErrNoMediaPause
	}
	return a.sendMediaRecv(&cast.MediaHeader{
		PayloadHeader:  cast.PauseHeader,
		MediaSessionId: a.media.MediaSessionId,
	})
}

func (a *Application) Unpause() error {
	if a.media == nil {
		return ErrNoMediaUnpause
	}
	return a.sendMediaRecv(&cast.MediaHeader{
		PayloadHeader:  cast.PlayHeader,
		MediaSessionId: a.media.MediaSessionId,
	})
}

func (a *Application) TogglePause() error {
	if a.media == nil {
		return ErrNoMediaTogglePause
	}
	switch a.media.PlayerState {
	case "PLAYING", "BUFFERING":
		{
			return a.Pause()
		}
	default:
		{
			return a.Unpause()
		}
	}
}

func (a *Application) Skipad() error {
	if a.media == nil {
		return ErrNoMediaSkip
	}
	if a.media.CustomData.PlayerState != 1081 {
		return ErrNoMediaSkipad
	}

	var result error
	MAX_LOOP := a.skipadRetries
	for a.media.CustomData.PlayerState == 1081 {
		result = a.sendMediaRecv(&cast.MediaHeader{
			PayloadHeader:  cast.SkipHeader,
			MediaSessionId: a.media.MediaSessionId,
		})
		// fmt.Printf("Looping because %d\n", a.media.CustomData.PlayerState)
		time.Sleep(a.skipadSleep)
		a.updateMediaStatus()
		MAX_LOOP--
		if MAX_LOOP == 0 {
			return ErrAdMaxLoop
		}
	}
	return result
}

func (a *Application) StopMedia() error {
	if a.media == nil {
		return ErrNoMediaStop
	}
	return a.sendMediaRecv(&cast.MediaHeader{
		PayloadHeader:  cast.StopHeader,
		MediaSessionId: a.media.MediaSessionId,
	})
}

func (a *Application) Stop() error {
	return a.sendDefaultRecv(&cast.StopHeader)
}

func (a *Application) Next() error {
	if a.media == nil {
		return ErrNoMediaNext
	}

	// TODO(vishen): Get the number of queue items, if none, possibly just skip to the end?
	return a.sendMediaRecv(&cast.QueueUpdate{
		PayloadHeader:  cast.QueueUpdateHeader,
		MediaSessionId: a.media.MediaSessionId,
		Jump:           1,
	})
}

func (a *Application) Previous() error {
	if a.media == nil {
		return ErrNoMediaPrevious
	}

	// TODO(vishen): Get the number of queue items, if none, possibly just jump to beginning?
	return a.sendMediaRecv(&cast.QueueUpdate{
		PayloadHeader:  cast.QueueUpdateHeader,
		MediaSessionId: a.media.MediaSessionId,
		Jump:           -1,
	})
}

func (a *Application) Skip() error {

	if a.media == nil {
		return ErrNoMediaSkip
	}

	// Get the latest media status
	// TODO(vishen): can we unroll this, so it doesn't update the current state?
	// but just returns it?
	// that might also make a.media == nil checks pointless?
	a.updateMediaStatus()

	v := a.media.CurrentTime - 10
	if a.media.Media.Duration > 0 {
		v = a.media.Media.Duration - 10
	}

	return a.Seek(int(v))
}

func (a *Application) Seek(value int) error {
	if a.media == nil {
		return ErrMediaNotYetInitialised
	}

	// TODO: find a better way to handle when chromecast
	// apps don't handle certain commands.
	appsSeekTo := []string{
		"9AC194DC", // Plex
		"CC1AD845", // Default media
	}

	if slices.Contains(appsSeekTo, a.application.AppId) {
		absolute := a.media.CurrentTime + float32(value)
		return a.SeekToTime(absolute)
	}

	return a.sendMediaRecv(&cast.MediaHeader{
		PayloadHeader:  cast.SeekHeader,
		MediaSessionId: a.media.MediaSessionId,
		RelativeTime:   float32(value),
		ResumeState:    "PLAYBACK_START",
	})
}

func (a *Application) SeekFromStart(value int) error {
	if a.media == nil {
		return ErrMediaNotYetInitialised
	}

	// Get the latest media status
	// TODO(vishen): can we unroll this, so it doesn't update the current state?
	// but just returns it?
	// that might also make a.media == nil checks pointless?
	a.updateMediaStatus()

	// TODO(vishen): maybe there is another ResumeState that lets us
	// seek from the end? Although not sure how this works for live media?

	return a.sendMediaRecv(&cast.MediaHeader{
		PayloadHeader:  cast.SeekHeader,
		MediaSessionId: a.media.MediaSessionId,
		CurrentTime:    float32(value),
		ResumeState:    "PLAYBACK_START",
	})
}

func (a *Application) SeekToTime(value float32) error {
	if a.media == nil {
		return ErrMediaNotYetInitialised
	}

	return a.sendMediaRecv(&cast.MediaHeader{
		PayloadHeader:  cast.SeekHeader,
		MediaSessionId: a.media.MediaSessionId,
		CurrentTime:    value,
		ResumeState:    "PLAYBACK_START",
	})
}

func (a *Application) SetVolume(value float32) error {
	if value > 1 || value < 0 {
		return ErrVolumeOutOfRange
	}

	return a.sendDefaultRecv(&cast.SetVolume{
		PayloadHeader: cast.VolumeHeader,
		Volume: cast.Volume{
			Level: value,
		},
	})
}

func (a *Application) SetMuted(value bool) error {
	return a.sendDefaultRecv(&cast.SetVolume{
		PayloadHeader: cast.VolumeHeader,
		Volume: cast.Volume{
			Muted: value,
		},
	})
}

func (a *Application) getMediaStatus() (*cast.MediaStatusResponse, error) {
	apiMessage, err := a.sendAndWaitMediaRecv(&cast.GetStatusHeader)
	if err != nil {
		return nil, err
	}
	var response cast.MediaStatusResponse
	if err := json.Unmarshal([]byte(*apiMessage.PayloadUtf8), &response); err != nil {
		return nil, errors.Wrap(err, "error unmarshaling json")
	}
	return &response, nil
}

func (a *Application) getReceiverStatus() (*cast.ReceiverStatusResponse, error) {
	apiMessage, err := a.sendAndWaitDefaultRecv(&cast.GetStatusHeader)
	if err != nil {
		return nil, err
	}
	var response cast.ReceiverStatusResponse
	if err := json.Unmarshal([]byte(*apiMessage.PayloadUtf8), &response); err != nil {
		return nil, errors.Wrap(err, "error unmarshaling json")
	}
	return &response, nil

}

func (a *Application) PlayableMediaType(filename string) bool {
	if a.knownFileType(filename) {
		return true
	}

	switch path.Ext(filename) {
	case ".avi":
		return true
	}

	return false
}

func (a *Application) possibleContentType(filename string) (string, error) {
	// If filename is an URL returns the content-type from the HEAD response headers
	// Otherwise returns the content-type thanks to filetype package
	var contentType string

	if strings.Contains(filename, "://") {
		req, err := http.NewRequest("HEAD", filename, nil)
		if err != nil {
			return "", errors.Wrap(err, "NewRequest failed")
		}
		client := &http.Client{}
		res, err := client.Do(req)
		if err != nil {
			return "", errors.Wrap(err, "do request failed")
		}
		contentType = res.Header.Get("Content-Type")
	} else {
		match, err := filetype.MatchFile(filename)
		if err != nil {
			return "", errors.Wrap(err, "match failed")
		}
		contentType = match.MIME.Value
	}
	if contentType == "" {
		if strings.Contains(filename, "://") && strings.Contains(filename, "?") {
			parts := strings.Split(filename, "?")
			filename = parts[0]
		}

		switch ext := strings.ToLower(path.Ext(filename)); ext {
		case ".jpg", ".jpeg":
			return "image/jpeg", nil
		case ".gif":
			return "image/gif", nil
		case ".bmp":
			return "image/bmp", nil
		case ".png":
			return "image/png", nil
		case ".webp":
			return "image/webp", nil
		case ".mp4", ".m4a", ".m4p":
			return "video/mp4", nil
		case ".webm":
			return "video/webm", nil
		case ".mp3":
			return "audio/mp3", nil
		case ".flac":
			return "audio/flac", nil
		case ".wav":
			return "audio/wav", nil
		case ".m3u8":
			return "application/x-mpegURL", nil
		default:
			return "", fmt.Errorf("unknown file extension %q", ext)
		}
	}
	return contentType, nil
}

func (a *Application) knownFileType(filename string) bool {
	if ct, _ := a.possibleContentType(filename); ct != "" {
		return true
	}
	return false
}

func (a *Application) castPlayableContentType(contentType string) bool {
	// https://developers.google.com/cast/docs/media
	switch contentType {
	case "image/apng", "image/bmp", "image/gif", "image/jpeg", "image/png", "image/webp":
		return true
	case "audio/mp2t", "audio/mpeg", "audio/mp3", "audio/mp4", "audio/ogg", "audio/wav", "audio/webm":
		return true
	case "video/mp4", "video/webm":
		return true
	}

	return false
}

func (a *Application) PlayedItems() map[string]PlayedItem {
	return a.playedItems
}

func (a *Application) Load(filenameOrUrl string, startTime int, contentType string, transcode, detach, forceDetach bool) error {
	// if the file is a playlist, ".pls", then just play the first item.
	if playlists.IsPlaylist(filenameOrUrl) {
		if strings.HasPrefix(filenameOrUrl, "./") { // convert to file:// uri
			if abs, err := filepath.Abs(filenameOrUrl); err != nil {
				return err
			} else {
				filenameOrUrl = fmt.Sprintf("file://%v", abs)
			}
		}
		it, err := playlists.NewIterator(filenameOrUrl)
		if err != nil {
			return err
		}
		var items []mediaItem
		for it.HasNext() {
			url, title := it.Next()
			items = append(items, mediaItem{
				filename:   url,
				contentURL: url,
			})
			log.Infof("Adding url %v (%v)", url, title)
		}
		return a.QueueLoadItems(items, "")
	}
	return a.play(filenameOrUrl, startTime, contentType, transcode, detach, forceDetach)
}

func (a *Application) play(filenameOrUrl string, startTime int, contentType string, transcode, detach, forceDetach bool) error {

	var mi mediaItem
	isExternalMedia := false
	if strings.HasPrefix(filenameOrUrl, "http://") || strings.HasPrefix(filenameOrUrl, "https://") {
		isExternalMedia = true
		if contentType == "" {
			// Try and determine the content type, but if we can't,
			// let the chromecast try and handle the media file anyway.
			contentType, _ = a.possibleContentType(filenameOrUrl)
		}
		mi = mediaItem{
			contentURL:  filenameOrUrl,
			contentType: contentType,
		}
	} else {
		mediaItems, err := a.loadAndServeFiles([]string{filenameOrUrl}, contentType, transcode)
		if err != nil {
			return errors.Wrap(err, "unable to load and serve files")
		}

		if len(mediaItems) != 1 {
			return fmt.Errorf("was expecting 1 media item, received %d", len(mediaItems))
		}
		mi = mediaItems[0]
	}

	if !forceDetach && !isExternalMedia && detach {
		return fmt.Errorf("unable to detach from locally playing media content")
	}

	if err := a.ensureIsDefaultMediaReceiver(); err != nil {
		return err
	}

	// NOTE: This isn't concurrent safe, but it doesn't need to be at the moment!
	a.MediaStart()

	// Send the command to the chromecast
	a.sendMediaRecv(&cast.LoadMediaCommand{
		PayloadHeader: cast.LoadHeader,
		CurrentTime:   startTime,
		Autoplay:      true,
		Media: cast.MediaItem{
			ContentId:   mi.contentURL,
			StreamType:  "BUFFERED",
			ContentType: mi.contentType,
		},
	})

	// If we should detach from waiting for media to finish playing
	// and this is a url loaded external media, then we can exit early.
	if (detach && isExternalMedia) || forceDetach {
		return nil
	}

	// Wait until we have been notified that the media has finished playing
	a.MediaWait()
	return nil
}

func (a *Application) LoadApp(appID, contentID string) error {
	// old list https://gist.github.com/jloutsenhizer/8855258.
	// NOTE: This isn't concurrent safe, but it doesn't need to be at the moment!
	a.MediaStart()

	if err := a.ensureIsAppID(appID); err != nil {
		return errors.Wrapf(err, "unable to change chromecast app")
	}

	// Send the command to the chromecast
	a.sendMediaRecv(&cast.LoadMediaCommand{
		PayloadHeader: cast.LoadHeader,
		CurrentTime:   0,
		Autoplay:      true,
		Media: cast.MediaItem{
			ContentId:  contentID,
			StreamType: "BUFFERED",
		},
	})

	// Wait until we have been notified that the media has finished playing
	a.MediaWait()

	return nil
}

func (a *Application) QueueLoad(filenames []string, contentType string, transcode bool) error {
	mediaItems, err := a.loadAndServeFiles(filenames, contentType, transcode)
	if err != nil {
		return errors.Wrap(err, "unable to load and serve files")
	}
	return a.QueueLoadItems(mediaItems, contentType)
}

func (a *Application) QueueLoadItems(mediaItems []mediaItem, contentType string) error {

	if err := a.ensureIsDefaultMediaReceiver(); err != nil {
		return err
	}

	items := make([]cast.QueueLoadItem, len(mediaItems))
	for i, mi := range mediaItems {
		items[i] = cast.QueueLoadItem{
			Autoplay:         true,
			PlaybackDuration: 60,
			Media: cast.MediaItem{
				ContentId:   mi.contentURL,
				StreamType:  "BUFFERED",
				ContentType: mi.contentType,
			},
		}
	}

	// Send the command to the chromecast
	a.sendMediaRecv(&cast.QueueLoad{
		PayloadHeader: cast.QueueLoadHeader,
		CurrentTime:   0,
		StartIndex:    0,
		RepeatMode:    "REPEAT_OFF",
		Items:         items,
	})

	// Wait until we have been notified that the media has finished playing
	// TODO: This does nothing. This hasn't been initialised and just blocks
	// forever.
	a.MediaWait()
	return nil
}

func (a *Application) ensureIsDefaultMediaReceiver() error {
	// If the current chromecast application isn't the Default Media Receiver
	// we need to change it.
	return a.ensureIsAppID(defaultChromecastAppID)
}

func (a *Application) ensureIsAppID(appID string) error {
	if a.application == nil || a.application.AppId != appID {
		_, err := a.sendAndWaitDefaultRecv(&cast.LaunchRequest{
			PayloadHeader: cast.LaunchHeader,
			AppId:         appID,
		})

		if err != nil {
			return errors.Wrapf(err, "unable to change to appID %q", appID)
		}
		// Update the 'application' and 'media' field on the 'CastApplication'
		return a.Update()
	}
	return nil
}

func (a *Application) Slideshow(filenames []string, duration int, repeat bool) error {
	mediaItems, err := a.loadAndServeFiles(filenames, "", false)
	if err != nil {
		return errors.Wrap(err, "unable to load and serve files")
	}

	if err := a.ensureIsDefaultMediaReceiver(); err != nil {
		return err
	}

	items := make([]cast.QueueLoadItem, len(mediaItems))
	for i, mi := range mediaItems {
		items[i] = cast.QueueLoadItem{
			Autoplay:         true,
			PlaybackDuration: duration,
			Media: cast.MediaItem{
				ContentId:   mi.contentURL,
				StreamType:  "BUFFERED",
				ContentType: mi.contentType,
			},
		}
	}

	var repeatMode string
	if repeat {
		repeatMode = "REPEAT_ALL"
	} else {
		repeatMode = "REPEAT_OFF"
	}

	// Send the command to the chromecast
	a.sendMediaRecv(&cast.QueueLoad{
		PayloadHeader: cast.QueueLoadHeader,
		CurrentTime:   0,
		StartIndex:    0,
		RepeatMode:    repeatMode,
		Items:         items,
	})

	// Timer for when to call the next image
	t := time.NewTicker(time.Second * time.Duration(duration))
	i := len(filenames)
	for {
		//  If we are not repeating, we need to stop after we have show the last image.
		if !repeat {
			if i == 0 {
				break
			}
			i--
		}
		select {
		case <-t.C:
			if err := a.Update(); err != nil {
				return err
			}
			// This is a hack because I can't work out how to
			// get the chromecast to automatically change a photo
			// in a slideshow. There is some documentation that
			// implies it should be possible but I was not able to make it work.
			// https://developers.google.com/cast/docs/reference/caf_receiver/cast.framework.messages.QueueItem.html#playbackDuration
			if err := a.Next(); err != nil {
				return err
			}
		// Media has finished playing.
		// TODO: Hack, how to ensure this is always possible
		// to wait on.
		case <-a.mediaFinished:
			return nil
		}
	}
	return nil
}

type mediaItem struct {
	filename    string
	contentType string
	contentURL  string
	transcode   bool
}

func (a *Application) loadAndServeFiles(filenames []string, contentType string, transcode bool) ([]mediaItem, error) {
	mediaItems := make([]mediaItem, len(filenames))
	for i, filename := range filenames {
		transcodeFile := transcode
		if _, err := os.Stat(filename); err != nil {
			return nil, errors.Wrapf(err, "unable to find %q", filename)
		}
		/*
			We can play media for the following:

			- if we have a filename with a known content type
			- if we have a filename, and a specified contentType
			- if we have a filename with an unknown content type, and transcode is true
			-
		*/
		knownFileType := a.knownFileType(filename)
		if !knownFileType && contentType == "" && !transcodeFile {
			return nil, fmt.Errorf("unknown content-type for %q, either specify a content-type or set transcode to true", filename)
		}

		// If we have a content-type specified we should always
		// attempt to use that
		contentTypeToUse := contentType
		if contentType != "" {
		} else if knownFileType {
			// If this is a media file we know the chromecast can play,
			// then we don't need to transcode it.
			contentTypeToUse, _ = a.possibleContentType(filename)
			if a.castPlayableContentType(contentTypeToUse) {
				transcodeFile = false
			}
		} else if transcodeFile {
			contentTypeToUse = "video/mp4"
		}

		mediaItems[i] = mediaItem{
			filename:    filename,
			contentType: contentTypeToUse,
			transcode:   transcodeFile,
		}
		// Add the filename to the list of filenames that go-chromecast will serve.
		a.mediaFilenames = append(a.mediaFilenames, filename)
	}

	localIP, err := a.getLocalIP()
	if err != nil {
		return nil, err
	}
	a.log("local IP address: %s", localIP)

	a.log("starting streaming server...")
	// Start server to serve the media
	if err := a.startStreamingServer(); err != nil {
		return nil, errors.Wrap(err, "unable to start streaming server")
	}
	a.log("started streaming server")

	// We can only set the content url after the server has started, otherwise we have
	// no way to know the port used.
	for i, m := range mediaItems {
		mediaItems[i].contentURL = fmt.Sprintf("http://%s:%d?media_file=%s&live_streaming=%t", localIP, a.serverPort, m.filename, m.transcode)
	}

	return mediaItems, nil
}

func (a *Application) getLocalIP() (string, error) {
	if a.localIP != "" {
		return a.localIP, nil
	}

	// If we aren't looking for an address on a certain network
	// interface, then we can use the local address of the connection.
	if a.iface == nil {
		var err error
		a.localIP, err = a.conn.LocalAddr()
		return a.localIP, errors.Wrap(err, "unable to get local addr from cast connection")
	}

	addrs, err := a.iface.Addrs()
	if err != nil {
		return "", err
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			// TODO(vishen): Fallback to ipv6 if ipv4 fails? Or maybe do the other
			// way around? I am unsure if chromecast supports ipv6???
			if ipnet.IP.To4() != nil {
				a.localIP = ipnet.IP.String()
				return a.localIP, nil
			}
		}
	}
	return "", fmt.Errorf("Failed to get local ip address")
}

func (a *Application) startStreamingServer() error {
	if a.httpServer != nil {
		return nil
	}
	a.log("trying to find available port to start streaming server on")

	listener, err := net.Listen("tcp", ":"+strconv.Itoa(a.serverPort))
	if err != nil {
		return errors.Wrap(err, "unable to bind to local tcp address")
	}

	a.serverPort = listener.Addr().(*net.TCPAddr).Port
	a.log("found available port :%d", a.serverPort)

	a.httpServer = http.NewServeMux()

	a.httpServer.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Check to see if we have a 'filename' and if it is one of the ones that have
		// already been validated and is useable.
		filename := r.URL.Query().Get("media_file")
		canServe := false
		for _, fn := range a.mediaFilenames {
			if fn == filename {
				canServe = true
			}
		}

		a.playedItems[filename] = PlayedItem{ContentID: filename, Started: time.Now().Unix()}
		a.writePlayedItems()

		// Check to see if this is a live streaming video and we need to use an
		// infinite range request / response. This comes from media that is either
		// live or currently being transcoded to a different media format.
		liveStreaming := false
		if ls := r.URL.Query().Get("live_streaming"); ls == "true" {
			liveStreaming = true
		}

		a.log("canServe=%t, liveStreaming=%t, filename=%s", canServe, liveStreaming, filename)
		if canServe {
			if !liveStreaming {
				http.ServeFile(w, r, filename)
			} else {
				a.serveLiveStreaming(w, r, filename)
			}
		} else {
			http.Error(w, "Invalid file", 400)
		}
		a.log("method=%s, headers=%v, reponse_headers=%v", r.Method, r.Header, w.Header())
		pi := a.playedItems[filename]

		// TODO(vishen): make this a pointer?
		pi.Finished = time.Now().Unix()
		a.playedItems[filename] = pi
		a.writePlayedItems()
	})

	go func() {
		a.log("media server listening on %d", a.serverPort)
		if err := http.Serve(listener, a.httpServer); err != nil && err != http.ErrServerClosed {
			log.WithField("package", "application").WithError(err).Fatal("error serving HTTP")
		}
	}()

	return nil
}

func (a *Application) serveLiveStreaming(w http.ResponseWriter, r *http.Request, filename string) {
	cmd := exec.Command(
		"ffmpeg",
		"-re", // encode at 1x playback speed, to not burn the CPU
		"-i", filename,
		"-vcodec", "h264",
		"-acodec", "aac",
		"-ac", "2", // chromecasts don't support more than two audio channels
		"-f", "mp4",
		"-movflags", "frag_keyframe+faststart",
		"-strict", "-experimental",
		"pipe:1",
	)

	cmd.Stdout = w
	if a.debug {
		cmd.Stderr = os.Stderr
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Transfer-Encoding", "chunked")

	if err := cmd.Run(); err != nil {
		log.WithField("package", "application").WithFields(log.Fields{
			"filename": filename,
		}).WithError(err).Error("error transcoding")
	}
}

func (a *Application) log(message string, args ...any) {
	if a.debug {
		log.WithField("package", "application").Infof(message, args...)
	}
}

func (a *Application) send(payload cast.Payload, sourceID, destinationID, namespace string) (int, error) {
	// NOTE: Not concurrent safe, but currently only synchronous flow is possible
	// TODO(vishen): just make concurrent safe regardless of current flow
	requestID += 1
	payload.SetRequestId(requestID)
	return requestID, a.conn.Send(requestID, payload, sourceID, destinationID, namespace)
}

func (a *Application) sendAndWait(payload cast.Payload, sourceID, destinationID, namespace string) (*pb.CastMessage, error) {
	requestID, err := a.send(payload, sourceID, destinationID, namespace)
	if err != nil {
		return nil, err
	}

	// Set a timeout to wait for the response
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	// TODO(vishen): not concurrent safe. Not a problem at the moment
	// because only synchronous flow currently allowed.
	resultChan := make(chan *pb.CastMessage, 1)
	a.resultChanMap[requestID] = resultChan
	defer func() {
		delete(a.resultChanMap, requestID)
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-resultChan:
		return result, nil
	}
}

// TODO(vishen): needing send(AndWait)* method seems a bit clunky, is there a better approach?
// Maybe having a struct that has send and sendAndWait, similar to before.
func (a *Application) sendDefaultConn(payload cast.Payload) error {
	_, err := a.send(payload, defaultSender, defaultRecv, namespaceConn)
	return err
}

func (a *Application) sendDefaultRecv(payload cast.Payload) error {
	_, err := a.send(payload, defaultSender, defaultRecv, namespaceRecv)
	return err
}

func (a *Application) sendMediaConn(payload cast.Payload) error {
	if a.application == nil {
		return ErrApplicationNotSet
	}
	_, err := a.send(payload, defaultSender, a.application.TransportId, namespaceConn)
	return err
}

func (a *Application) sendMediaRecv(payload cast.Payload) error {
	if a.application == nil {
		return ErrApplicationNotSet
	}
	_, err := a.send(payload, defaultSender, a.application.TransportId, namespaceMedia)
	return err
}

func (a *Application) sendAndWaitDefaultConn(payload cast.Payload) (*pb.CastMessage, error) {
	return a.sendAndWait(payload, defaultSender, defaultRecv, namespaceConn)
}

func (a *Application) sendAndWaitDefaultRecv(payload cast.Payload) (*pb.CastMessage, error) {
	return a.sendAndWait(payload, defaultSender, defaultRecv, namespaceRecv)
}

func (a *Application) sendAndWaitMediaConn(payload cast.Payload) (*pb.CastMessage, error) {
	if a.application == nil {
		return nil, ErrApplicationNotSet
	}
	return a.sendAndWait(payload, defaultSender, a.application.TransportId, namespaceConn)
}

func (a *Application) sendAndWaitMediaRecv(payload cast.Payload) (*pb.CastMessage, error) {
	if a.application == nil {
		return nil, ErrApplicationNotSet
	}
	return a.sendAndWait(payload, defaultSender, a.application.TransportId, namespaceMedia)
}

func (a *Application) startTranscodingServer(command string, args ...string) error {
	if a.httpServer != nil {
		return nil
	}
	a.log("trying to find available port to start transcoding server on")

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return errors.Wrap(err, "unable to bind to local tcp address")
	}

	a.serverPort = listener.Addr().(*net.TCPAddr).Port
	a.log("found available port :%d", a.serverPort)

	a.httpServer = http.NewServeMux()

	a.httpServer.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Check to see if we have a 'filename' and if it is one of the ones that have
		// already been validated and is useable.
		filename := r.URL.Query().Get("media_file")
		canServe := false
		for _, fn := range a.mediaFilenames {
			if fn == filename {
				canServe = true
			}
		}

		a.playedItems[filename] = PlayedItem{ContentID: filename, Started: time.Now().Unix()}
		a.writePlayedItems()

		a.log("canServe=%t, liveStreaming=%t, filename=%s", canServe, true, filename)
		if canServe {
			cmd := exec.Command(command, args...)

			cmd.Stdout = w
			if a.debug {
				cmd.Stderr = os.Stderr
			}

			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Transfer-Encoding", "chunked")

			if err := cmd.Run(); err != nil {
				log.WithField("package", "application").WithFields(log.Fields{
					"filename": filename,
				}).WithError(err).Error("error transcoding")
			}
		} else {
			http.Error(w, "Invalid file", 400)
		}
		a.log("method=%s, headers=%v, reponse_headers=%v", r.Method, r.Header, w.Header())
		pi := a.playedItems[filename]

		// TODO(vishen): make this a pointer?
		pi.Finished = time.Now().Unix()
		a.playedItems[filename] = pi
		a.writePlayedItems()
	})

	go func() {
		a.log("media server listening on %d", a.serverPort)
		if err := http.Serve(listener, a.httpServer); err != nil && err != http.ErrServerClosed {
			log.WithField("package", "application").WithError(err).Fatal("error serving HTTP")
		}
	}()

	return nil
}

func (a *Application) Transcode(contentType string, command string, args ...string) error {

	if command == "" || contentType == "" {
		return errors.New("command and content-type flags needs to be set when transcoding")
	}

	filename := "pipe_output"
	// Add the filename to the list of filenames that go-chromecast will serve.
	a.mediaFilenames = append(a.mediaFilenames, filename)

	localIP, err := a.getLocalIP()
	if err != nil {
		return err
	}
	a.log("local IP address: %s", localIP)

	a.log("starting transcoding server...")
	// Start server to serve the media
	if err := a.startTranscodingServer(command, args...); err != nil {
		return errors.Wrap(err, "unable to start transcoding server")
	}
	a.log("started transcoding server")

	// We can only set the content url after the server has started, otherwise we have
	// no way to know the port used.
	contentURL := fmt.Sprintf("http://%s:%d?media_file=%s", localIP, a.serverPort, filename)

	if err := a.ensureIsDefaultMediaReceiver(); err != nil {
		return err
	}

	// NOTE: This isn't concurrent safe, but it doesn't need to be at the moment!
	a.MediaStart()

	// Send the command to the chromecast
	a.sendMediaRecv(&cast.LoadMediaCommand{
		PayloadHeader: cast.LoadHeader,
		CurrentTime:   0,
		Autoplay:      true,
		Media: cast.MediaItem{
			ContentId:   contentURL,
			StreamType:  "BUFFERED",
			ContentType: contentType,
		},
	})

	// Wait until we have been notified that the media has finished playing
	a.MediaWait()
	return nil
}

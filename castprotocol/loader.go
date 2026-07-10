package castprotocol

import (
	"fmt"
	"strings"
	"sync/atomic"

	"go2tv.app/go2tv/v2/castprotocol/v2/cast"
	"go2tv.app/go2tv/v2/metadata"
)

// Request ID counter for Chromecast messages
var requestIDCounter int32

func nextRequestID() int {
	return int(atomic.AddInt32(&requestIDCounter, 1))
}

// TextTrackStyle defines the appearance of subtitle text on Chromecast.
type TextTrackStyle struct {
	BackgroundColor string  `json:"backgroundColor,omitempty"` // ARGB format e.g. "#00000000"
	ForegroundColor string  `json:"foregroundColor,omitempty"` // ARGB format e.g. "#FFFFFFFF"
	EdgeType        string  `json:"edgeType,omitempty"`        // "NONE", "OUTLINE", "DROP_SHADOW", etc.
	EdgeColor       string  `json:"edgeColor,omitempty"`       // ARGB format
	FontScale       float32 `json:"fontScale,omitempty"`       // Font size multiplier
}

// This extends the standard cast.LoadMediaCommand to include subtitle tracks.
type CustomLoadPayload struct {
	Type           string              `json:"type"`
	RequestId      int                 `json:"requestId"`
	Media          MediaItemWithTracks `json:"media"`
	CurrentTime    *float64            `json:"currentTime,omitempty"` // Omit for LIVE to start at live edge
	Autoplay       bool                `json:"autoplay"`
	ActiveTrackIds []int               `json:"activeTrackIds,omitempty"`
}

// SetRequestId implements cast.Payload interface
func (p *CustomLoadPayload) SetRequestId(id int) {
	p.RequestId = id
}

// LoadWithSubtitles sends a custom LOAD command with subtitle tracks to the Chromecast.
// This is called after the Application has connected and launched the default media receiver.
// conn: the cast connection (get from app's internal connection)
// transportId: the media receiver's transport ID
// mediaURL: URL of the media to play
// contentType: MIME type of the media
// startTime: start position in seconds
// duration: total media duration in seconds (0 to let Chromecast detect)
// subtitleURL: URL of the WebVTT subtitle file (or empty for no subtitles)
// title: media title shown by the receiver UI
// live: if true, sets StreamType to "LIVE" to identify as live stream (DMR will show LIVE badge)
// autoplay: if true, starts playback immediately; if false, waits for PLAY command
func LoadWithSubtitles(conn cast.Conn, transportId string, mediaURL string, contentType string, startTime int, duration float64, subtitleURL string, title string, live bool, autoplay bool) error {
	return loadMedia(conn, transportId, LoadRequest{
		MediaURL:    mediaURL,
		ContentType: contentType,
		Metadata:    metadata.Media{Title: title},
		StartTime:   startTime,
		Duration:    duration,
		SubtitleURL: subtitleURL,
		Live:        live,
	}, autoplay)
}

func loadMedia(conn cast.Conn, transportId string, req LoadRequest, autoplay bool) error {
	streamType := "BUFFERED"
	if req.Live {
		streamType = "LIVE"
	}

	mediaItem := MediaItemWithTracks{
		ContentId:   req.MediaURL,
		ContentType: req.ContentType,
		StreamType:  streamType,
	}

	// Set duration if provided (useful for transcoded streams where Chromecast can't detect it)
	if req.Duration > 0 {
		mediaItem.Duration = float32(req.Duration)
	}
	if hasMediaMetadata(req.Metadata) {
		mediaItem.Metadata = newMediaMeta(req.ContentType, req.Metadata)
	}

	var activeTrackIds []int

	if req.SubtitleURL != "" {
		// Add subtitle track
		subtitleTrack := NewSubtitleTrack(1, req.SubtitleURL, "Subtitles", "en")
		mediaItem.Tracks = []MediaTrack{subtitleTrack}
		activeTrackIds = []int{1} // Activate the subtitle track
	}

	// Add text track style to media
	mediaItem.TextTrackStyle = &TextTrackStyle{
		BackgroundColor: "#00000000", // Transparent
		FontScale:       1.0,
		EdgeType:        "OUTLINE",
		EdgeColor:       "#000000FF",
		ForegroundColor: "#FFFFFFFF", // White text
	}

	payload := &CustomLoadPayload{
		Type:           "LOAD",
		Media:          mediaItem,
		Autoplay:       autoplay,
		ActiveTrackIds: activeTrackIds,
	}

	// For LIVE streams, omitting currentTime makes Chromecast jump to live edge.
	// If startTime is explicitly set (>0), keep it.
	if !req.Live || req.StartTime > 0 {
		start := float64(req.StartTime)
		payload.CurrentTime = &start
	}

	requestID := nextRequestID()
	payload.SetRequestId(requestID)

	// Send to the media receiver
	// Namespace for media receiver is "urn:x-cast:com.google.cast.media"
	err := conn.Send(requestID, payload, "sender-0", transportId, "urn:x-cast:com.google.cast.media")
	if err != nil {
		return fmt.Errorf("send load with subtitles: %w", err)
	}

	return nil
}

func hasMediaMetadata(mediaMetadata metadata.Media) bool {
	return mediaMetadata.Title != "" ||
		mediaMetadata.Artist != "" ||
		mediaMetadata.Album != "" ||
		mediaMetadata.AlbumArtist != "" ||
		mediaMetadata.Artwork != nil
}

func newMediaMeta(contentType string, mediaMetadata metadata.Media) *MediaMeta {
	metadataType := 0
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "audio/") {
		metadataType = 3
	}

	castMetadata := &MediaMeta{
		MetadataType: metadataType,
		Title:        mediaMetadata.Title,
		Artist:       mediaMetadata.Artist,
		AlbumName:    mediaMetadata.Album,
		AlbumArtist:  mediaMetadata.AlbumArtist,
	}
	if mediaMetadata.Artwork != nil {
		castMetadata.Images = []MediaImage{{
			URL:    mediaMetadata.Artwork.URL,
			Width:  mediaMetadata.Artwork.Width,
			Height: mediaMetadata.Artwork.Height,
		}}
	}

	return castMetadata
}

// Ensure CustomLoadPayload implements the cast.Payload interface
var _ cast.Payload = (*CustomLoadPayload)(nil)

// LaunchRequest is a payload to launch an app on Chromecast without loading media.
type LaunchRequest struct {
	Type      string `json:"type"`
	RequestId int    `json:"requestId"`
	AppId     string `json:"appId"`
}

// SetRequestId implements cast.Payload interface
func (p *LaunchRequest) SetRequestId(id int) {
	p.RequestId = id
}

// LaunchDefaultReceiver launches the Default Media Receiver app without loading media.
// This allows sending a LoadWithSubtitles command afterwards.
func LaunchDefaultReceiver(conn cast.Conn) error {
	payload := &LaunchRequest{
		Type:  "LAUNCH",
		AppId: cast.DefaultMediaReceiverAppID,
	}

	requestID := nextRequestID()
	payload.SetRequestId(requestID)

	// Send to receiver namespace - destination is "receiver-0" for launching apps
	err := conn.Send(requestID, payload, "sender-0", "receiver-0", CastNamespaceReceiver)
	if err != nil {
		return fmt.Errorf("send launch request: %w", err)
	}

	return nil
}

// CastNamespaceReceiver is the namespace for receiver control messages
const CastNamespaceReceiver = "urn:x-cast:com.google.cast.receiver"

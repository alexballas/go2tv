package castprotocol

import (
	"encoding/json"
	"fmt"
	"sync/atomic"

	"github.com/vishen/go-chromecast/cast"
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
	CurrentTime    float64             `json:"currentTime"` // Seconds (SDK uses float)
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
// live: if true, sets StreamType to "LIVE" to identify as live stream (DMR will show LIVE badge)
// autoplay: if true, starts playback immediately; if false, waits for PLAY command
func LoadWithSubtitles(conn cast.Conn, transportId string, mediaURL string, contentType string, startTime int, duration float64, subtitleURL string, live bool, autoplay bool) error {
	streamType := "BUFFERED"
	if live {
		streamType = "LIVE"
	}

	media := MediaItemWithTracks{
		ContentId:   mediaURL,
		ContentType: contentType,
		StreamType:  streamType,
	}

	// Set duration if provided (useful for transcoded streams where Chromecast can't detect it)
	if duration > 0 {
		media.Duration = float32(duration)
	}

	var activeTrackIds []int

	if subtitleURL != "" {
		// Add subtitle track
		subtitleTrack := NewSubtitleTrack(1, subtitleURL, "Subtitles", "en")
		media.Tracks = []MediaTrack{subtitleTrack}
		activeTrackIds = []int{1} // Activate the subtitle track
	}

	// Add text track style to media
	media.TextTrackStyle = &TextTrackStyle{
		BackgroundColor: "#00000000", // Transparent
		FontScale:       1.0,
		EdgeType:        "OUTLINE",
		EdgeColor:       "#000000FF",
		ForegroundColor: "#FFFFFFFF", // White text
	}

	payload := &CustomLoadPayload{
		Type:           "LOAD",
		Media:          media,
		CurrentTime:    float64(startTime),
		Autoplay:       autoplay,
		ActiveTrackIds: activeTrackIds,
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

// DefaultMediaReceiverAppID is the app ID for the Default Media Receiver
const DefaultMediaReceiverAppID = "CC1AD845"

// LaunchDefaultReceiver launches the Default Media Receiver app without loading media.
// This allows sending a LoadWithSubtitles command afterwards.
func LaunchDefaultReceiver(conn cast.Conn) error {
	payload := &LaunchRequest{
		Type:  "LAUNCH",
		AppId: DefaultMediaReceiverAppID,
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

// MarshalJSON for custom JSON output
func (m *MediaItemWithTracks) MarshalJSON() ([]byte, error) {
	type Alias MediaItemWithTracks
	return json.Marshal(&struct {
		*Alias
	}{
		Alias: (*Alias)(m),
	})
}

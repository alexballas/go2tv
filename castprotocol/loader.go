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

// CustomLoadPayload is a LoadMediaCommand with tracks support.
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
// subtitleURL: URL of the WebVTT subtitle file (or empty for no subtitles)
func LoadWithSubtitles(conn cast.Conn, transportId string, mediaURL string, contentType string, startTime int, subtitleURL string) error {
	media := MediaItemWithTracks{
		ContentId:   mediaURL,
		ContentType: contentType,
		StreamType:  "BUFFERED",
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
		Autoplay:       true,
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

// MarshalJSON for custom JSON output
func (m *MediaItemWithTracks) MarshalJSON() ([]byte, error) {
	type Alias MediaItemWithTracks
	return json.Marshal(&struct {
		*Alias
	}{
		Alias: (*Alias)(m),
	})
}

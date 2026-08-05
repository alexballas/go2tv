package castprotocol

import "go2tv.app/go2tv/v2/metadata"

// LoadRequest describes one media load, including protocol-neutral metadata.
type LoadRequest struct {
	MediaURL    string
	ContentType string
	Metadata    metadata.Media
	StartTime   int
	Duration    float64
	SubtitleURL string
	Live        bool
}

// MediaTrack represents a media track (audio, video, or text/subtitles).
// For subtitles, use Type="TEXT" and SubType="SUBTITLES".
type MediaTrack struct {
	TrackId     int    `json:"trackId"`
	Type        string `json:"type"`             // "TEXT", "AUDIO", "VIDEO"
	SubType     string `json:"subtype"`          // "SUBTITLES", "CAPTIONS", etc.
	ContentId   string `json:"trackContentId"`   // URL to the track content (e.g., WebVTT file)
	ContentType string `json:"trackContentType"` // MIME type (e.g., "text/vtt")
	Name        string `json:"name"`             // Display name (e.g., "English Subtitles")
	Language    string `json:"language"`         // Language code (e.g., "en")
}

// MediaItemWithTracks extends MediaItem with tracks support for subtitles.
type MediaItemWithTracks struct {
	ContentId      string          `json:"contentId"`
	ContentType    string          `json:"contentType"`
	StreamType     string          `json:"streamType"`
	Duration       float32         `json:"duration,omitempty"`
	Metadata       *MediaMeta      `json:"metadata,omitempty"`
	Tracks         []MediaTrack    `json:"tracks,omitempty"`
	TextTrackStyle *TextTrackStyle `json:"textTrackStyle,omitempty"`
}

// MediaMeta contains metadata about the media.
type MediaMeta struct {
	MetadataType int          `json:"metadataType"`
	Title        string       `json:"title,omitempty"`
	Artist       string       `json:"artist,omitempty"`
	AlbumName    string       `json:"albumName,omitempty"`
	AlbumArtist  string       `json:"albumArtist,omitempty"`
	Images       []MediaImage `json:"images,omitempty"`
}

// MediaImage is a Chromecast metadata image.
type MediaImage struct {
	URL    string `json:"url"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// NewSubtitleTrack creates a MediaTrack configured for WebVTT subtitles.
func NewSubtitleTrack(trackId int, url, name, language string) MediaTrack {
	return MediaTrack{
		TrackId:     trackId,
		Type:        "TEXT",
		SubType:     "SUBTITLES",
		ContentId:   url,
		ContentType: "text/vtt",
		Name:        name,
		Language:    language,
	}
}

package webui

import (
	"encoding/json"

	"go2tv.app/go2tv/v2/internal/controller"
	"go2tv.app/go2tv/v2/internal/library"
)

const ProtocolVersion = 1

type Config struct {
	Version    string
	Controller *controller.Controller
	Library    *library.Library
	Artwork    *controller.ArtworkCache
	FFmpegPath string
	Logger     controller.EventLogger
}

type envelope struct {
	ProtocolVersion int             `json:"protocol_version"`
	Type            string          `json:"type"`
	ID              string          `json:"id,omitempty"`
	Payload         json.RawMessage `json:"payload,omitempty"`
}

type rootDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type entryDTO struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	MediaKind    string `json:"media_kind,omitempty"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	ArtworkURL   string `json:"artwork_url,omitempty"`
}

type bootstrapDTO struct {
	ServerVersion   string          `json:"server_version"`
	ProtocolVersion int             `json:"protocol_version"`
	Snapshot        snapshotDTO     `json:"snapshot"`
	Roots           []rootDTO       `json:"roots"`
	Limits          map[string]int  `json:"limits"`
	Features        map[string]bool `json:"features"`
}

type deviceDTO struct {
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	Protocol     string   `json:"protocol"`
	Capabilities []string `json:"capabilities,omitempty"`
}
type queueDTO struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Parent   string `json:"parent,omitempty"`
	Kind     string `json:"kind"`
	Selected bool   `json:"selected"`
	Active   bool   `json:"active"`
}
type snapshotDTO struct {
	Revision             uint64            `json:"revision"`
	Devices              []deviceDTO       `json:"devices"`
	SelectedDeviceID     string            `json:"selected_device_id,omitempty"`
	ActiveDeviceID       string            `json:"active_device_id,omitempty"`
	SelectedMedia        bool              `json:"selected_media"`
	SelectedMediaName    string            `json:"selected_media_name,omitempty"`
	ActiveMediaName      string            `json:"active_media_name,omitempty"`
	SelectedSubtitle     bool              `json:"selected_subtitle"`
	SelectedSubtitleName string            `json:"selected_subtitle_name,omitempty"`
	Queue                []queueDTO        `json:"queue"`
	Transcode            bool              `json:"transcode"`
	HasSession           bool              `json:"has_session"`
	PlaybackState        string            `json:"playback_state"`
	Position             int               `json:"position"`
	Duration             int               `json:"duration"`
	Volume               int               `json:"volume"`
	Muted                bool              `json:"muted"`
	MediaType            string            `json:"media_type,omitempty"`
	ArtworkID            string            `json:"artwork_id"`
	Policy               controller.Policy `json:"policy"`
}

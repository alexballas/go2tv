package servermode

// Public DTOs intentionally contain display-safe values only. Internal playback,
// device, transport, filesystem, and command types must never cross this boundary.
type DeviceDTO struct {
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type QueueItemDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type PlaybackStateDTO struct {
	State string `json:"state"`
}

type ErrorDTO struct {
	Error string `json:"error"`
}

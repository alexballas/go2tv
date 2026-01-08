package castprotocol

// CastStatus represents current Chromecast playback state.
type CastStatus struct {
	PlayerState string  // "PLAYING", "PAUSED", "IDLE", "BUFFERING"
	CurrentTime float32 // Current position in seconds
	Duration    float32 // Total duration in seconds
	Volume      float32 // Volume level (0.0 to 1.0)
	Muted       bool
	MediaTitle  string
	ContentType string
}

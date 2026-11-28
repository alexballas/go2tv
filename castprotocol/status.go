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
	StreamType  string // "BUFFERED" or "LIVE"
	// MediaSessionID changes on every LOAD; 0 means no media session.
	MediaSessionID int
	// IdleReason is FINISHED, CANCELLED, INTERRUPTED or ERROR when IDLE.
	IdleReason string
}

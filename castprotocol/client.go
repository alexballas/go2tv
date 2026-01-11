package castprotocol

import (
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/vishen/go-chromecast/application"
	"github.com/vishen/go-chromecast/cast"
)

// CastClient wraps go-chromecast Application for simplified API
type CastClient struct {
	app       *application.Application
	conn      cast.Conn // keep reference to connection for custom commands
	mu        sync.RWMutex
	host      string
	port      int
	connected bool
}

func NewCastClient(deviceAddr string) (*CastClient, error) {
	u, err := url.Parse(deviceAddr)
	if err != nil {
		return nil, fmt.Errorf("parse device addr: %w", err)
	}

	host := u.Hostname()
	port := 8009 // default Chromecast port
	if u.Port() != "" {
		fmt.Sscanf(u.Port(), "%d", &port)
	}

	// Create our own connection that we can use for custom commands
	conn := cast.NewConnection()

	// Create application with our connection and retry configuration
	app := application.NewApplication(
		application.WithConnection(conn),
		application.WithConnectionRetries(3), // Retry up to 3 times on connection failures
	)

	return &CastClient{
		app:  app,
		conn: conn,
		host: host,
		port: port,
	}, nil
}

// Connect establishes connection to the Chromecast device.
// The library handles retries internally with WithConnectionRetries(3).
func (c *CastClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.app.Start(c.host, c.port); err != nil {
		return fmt.Errorf("chromecast connect: %w", err)
	}
	c.connected = true
	return nil
}

// Load loads media from URL onto the Chromecast.
// startTime is the position in seconds to start playback from.
// duration is the total media duration in seconds (0 to let Chromecast detect).
// If subtitleURL is provided, uses custom load command with subtitle tracks.
func (c *CastClient) Load(mediaURL string, contentType string, startTime int, duration float64, subtitleURL string) error {
	// If no subtitles and no custom duration needed, use standard load
	if subtitleURL == "" && duration == 0 {
		if err := c.app.Load(mediaURL, startTime, contentType, false, false, false); err != nil {
			return err
		}
		return nil
	}

	// With subtitles or custom duration: launch the app first WITHOUT loading media, then send custom load
	// This prevents double playback (first without subs, then with subs queued)
	if err := LaunchDefaultReceiver(c.conn); err != nil {
		return fmt.Errorf("launch receiver: %w", err)
	}

	// Retry getting app state with backoff (handles "media receiver app not available")
	var transportId string
	for i := range 5 {
		if err := c.app.Update(); err != nil {
			time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
			continue
		}
		app := c.app.App()
		if app != nil && app.TransportId != "" {
			transportId = app.TransportId
			break
		}
		time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
	}

	return LoadWithSubtitles(c.conn, transportId, mediaURL, contentType, startTime, duration, subtitleURL)
}

// LoadOnExisting loads media on an already-running receiver (for seek operations).
// Unlike Load, this skips launching the receiver and the 2-second wait.
// Use this when the receiver is already playing media and you want to load new content.
func (c *CastClient) LoadOnExisting(mediaURL string, contentType string, startTime int, duration float64, subtitleURL string) error {
	// Retry getting app state with backoff (handles transient errors during seek)
	var transportId string
	for i := range 5 {
		if err := c.app.Update(); err != nil {
			time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
			continue
		}
		app := c.app.App()
		if app != nil && app.TransportId != "" {
			transportId = app.TransportId
			break
		}
		time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
	}

	return LoadWithSubtitles(c.conn, transportId, mediaURL, contentType, startTime, duration, subtitleURL)
}

// Play resumes playback.
func (c *CastClient) Play() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.app.Unpause()
}

// Pause pauses playback.
func (c *CastClient) Pause() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.app.Pause()
}

// Stop stops playback and closes the media session.
func (c *CastClient) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.app.Stop()
}

// Seek seeks to position in seconds from start.
func (c *CastClient) Seek(seconds int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.app.SeekFromStart(seconds)
}

// SetVolume sets volume (0.0 to 1.0).
func (c *CastClient) SetVolume(level float32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.app.SetVolume(level)
}

// SetMuted sets mute state.
func (c *CastClient) SetMuted(muted bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.app.SetMuted(muted)
}

// GetStatus returns current playback status.
// No mutex needed - only reads from underlying library which has its own sync.
func (c *CastClient) GetStatus() (*CastStatus, error) {
	// Request fresh status from device (Update refreshes the cached status)
	if err := c.app.Update(); err != nil {
		return nil, err
	}
	_, media, vol := c.app.Status()
	status := &CastStatus{}
	if vol != nil {
		status.Volume = float32(vol.Level)
		status.Muted = vol.Muted
	}
	if media != nil {
		status.PlayerState = media.PlayerState
		status.CurrentTime = media.CurrentTime
		if media.Media.Duration > 0 {
			status.Duration = media.Media.Duration
		}
		status.ContentType = media.Media.ContentType
		status.MediaTitle = media.Media.Metadata.Title
	} else {
		status.PlayerState = "IDLE"
	}
	return status, nil
}

// Close disconnects from the Chromecast device.
func (c *CastClient) Close(stopMedia bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.connected = false
	return c.app.Close(stopMedia)
}

// IsConnected returns whether client is connected.
// Uses RLock for read-only access to avoid blocking on mutex contention.
func (c *CastClient) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

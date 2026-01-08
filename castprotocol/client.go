package castprotocol

import (
	"fmt"
	"net/url"
	"sync"

	"github.com/vishen/go-chromecast/application"
	"github.com/vishen/go-chromecast/cast"
)

// CastClient wraps go-chromecast Application for simplified API
type CastClient struct {
	app       *application.Application
	conn      cast.Conn // keep reference to connection for custom commands
	mu        sync.Mutex
	host      string
	port      int
	connected bool
}

// NewCastClient creates a new Chromecast client for the given address.
// Address format: "http://192.168.1.100:8009" (as stored in Device.Addr)
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

	// Create application with our connection
	app := application.NewApplication(application.WithConnection(conn))

	return &CastClient{
		app:  app,
		conn: conn,
		host: host,
		port: port,
	}, nil
}

// Connect establishes connection to the Chromecast device.
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
// If subtitleURL is provided and the app is connected, uses custom load with subtitle track.
func (c *CastClient) Load(mediaURL string, contentType string, startTime int, subtitleURL string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// If subtitles are provided and we have the app launched, use custom load with tracks
	if subtitleURL != "" && c.app.App() != nil {
		transportId := c.app.App().TransportId
		if transportId != "" {
			return LoadWithSubtitles(c.conn, transportId, mediaURL, contentType, startTime, subtitleURL)
		}
	}

	// Fallback to standard load (no subtitles)
	// go-chromecast Load signature: (filenameOrUrl, startTime, contentType, transcode, detach, forceDetach)
	if err := c.app.Load(mediaURL, startTime, contentType, false, false, false); err != nil {
		return err
	}

	return nil
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
func (c *CastClient) GetStatus() (*CastStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

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
func (c *CastClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

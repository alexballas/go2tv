package castprotocol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/vishen/go-chromecast/application"
	"github.com/vishen/go-chromecast/cast"
)

// CastClient wraps go-chromecast Application for simplified API
type CastClient struct {
	app         *application.Application
	conn        cast.Conn // keep reference to connection for custom commands
	mu          sync.RWMutex
	host        string
	port        int
	connected   bool
	Logger      zerolog.Logger
	LogOutput   io.Writer
	initLogOnce sync.Once
}

// Log returns the zerolog logger, initializing it lazily if LogOutput is set.
// Same pattern as TVPayload.Log() in soapcalls/soapcallers.go.
func (c *CastClient) Log() *zerolog.Logger {
	if c.LogOutput != nil {
		c.initLogOnce.Do(func() {
			c.Logger = zerolog.New(c.LogOutput).With().Timestamp().Logger()
		})
	}
	return &c.Logger
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
		application.WithConnectionRetries(5), // Retry up to 5 times on connection failures (slow TVs need time to wake)
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

	if c.app == nil {
		return fmt.Errorf("chromecast connect: app is nil")
	}

	c.Log().Debug().Str("Method", "Connect").Str("Host", c.host).Int("Port", c.port).Msg("connecting")
	if err := c.app.Start(c.host, c.port); err != nil {
		c.Log().Error().Str("Method", "Connect").Err(err).Msg("connection failed")
		return fmt.Errorf("chromecast connect: %w", err)
	}
	c.connected = true
	c.Log().Debug().Str("Method", "Connect").Msg("connected successfully")
	return nil
}

// isTimeoutError checks if an error is a timeout/deadline exceeded error.
// This typically happens when the TV needs to wake from sleep.
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}

	// 1. Check for context timeouts (context deadline exceeded)
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	// 2. Check for network timeouts (net: i/o timeout)
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	return false
}

// Load loads media from URL onto the Chromecast.
// startTime is the position in seconds to start playback from.
// duration is the total media duration in seconds (0 to let Chromecast detect).
// If subtitleURL is provided, uses custom load command with subtitle tracks.
// If live is true, uses StreamType "LIVE" to identify as live stream.
func (c *CastClient) Load(mediaURL string, contentType string, startTime int, duration float64, subtitleURL string, live bool) error {
	c.Log().Debug().Str("Method", "Load").Str("URL", mediaURL).Str("ContentType", contentType).Int("StartTime", startTime).Float64("Duration", duration).Bool("HasSubs", subtitleURL != "").Bool("Live", live).Msg("loading media")

	// Check if connection is still active, reconnect if needed
	// This handles cases where Close() was called but the client is being reused
	if !c.IsConnected() {
		c.Log().Debug().Str("Method", "Load").Msg("connection closed, reconnecting")
		if err := c.Connect(); err != nil {
			return fmt.Errorf("reconnect before load: %w", err)
		}
	}

	// If no subtitles, no custom duration, and NOT a live stream: use standard app.Load()
	// For live streams, we MUST use custom LoadWithSubtitles to set StreamType "LIVE"
	// (go-chromecast library hardcodes StreamType "BUFFERED" so we need custom path for LIVE)
	if subtitleURL == "" && duration == 0 && !live {
		// Retry loop for TV wake-up scenarios (timeout errors)
		var lastErr error
		for attempt := range 5 {
			if !c.IsConnected() {
				c.Log().Debug().Str("Method", "Load").Msg("connection closed during load, aborting silently")
				return nil
			}
			if err := c.app.Load(mediaURL, startTime, contentType, false, false, false); err != nil {
				lastErr = err
				if isTimeoutError(err) && attempt < 5 {
					c.Log().Debug().Str("Method", "Load").Int("Attempt", attempt).Err(err).Msg("timeout, TV may be waking up, retrying...")
					if !c.IsConnected() {
						c.Log().Debug().Str("Method", "Load").Msg("connection closed during retry wait, aborting silently")
						return nil
					}
					time.Sleep(4 * time.Second) // Wait for TV to wake up
					continue
				}
				c.Log().Error().Str("Method", "Load").Err(err).Msg("standard load failed")
				return err
			}
			c.Log().Debug().Str("Method", "Load").Msg("standard load success")
			return nil
		}
		return lastErr
	}

	// With subtitles or custom duration: launch the app first WITHOUT loading media, then send custom load
	// This prevents double playback (first without subs, then with subs queued)
	// Retry loop for TV wake-up scenarios
	var lastErr error
	for attempt := range 5 {
		if !c.IsConnected() {
			c.Log().Debug().Str("Method", "Load").Msg("connection closed during load, aborting silently")
			return nil
		}

		c.Log().Debug().Str("Method", "Load").Int("Attempt", attempt).Msg("launching default receiver")
		if err := LaunchDefaultReceiver(c.conn); err != nil {
			lastErr = err
			if isTimeoutError(err) && attempt < 5 {
				c.Log().Debug().Str("Method", "Load").Int("Attempt", attempt).Err(err).Msg("timeout, TV may be waking up, retrying...")
				if !c.IsConnected() {
					c.Log().Debug().Str("Method", "Load").Msg("connection closed during retry wait, aborting silently")
					return nil
				}
				time.Sleep(4 * time.Second)
				continue
			}
			c.Log().Error().Str("Method", "Load").Err(err).Msg("launch receiver failed")
			return fmt.Errorf("launch receiver: %w", err)
		}

		// Retry getting app state with backoff (handles "media receiver app not available")
		var transportId string
		for i := range 8 {
			if !c.IsConnected() {
				c.Log().Debug().Str("Method", "Load").Msg("connection closed during app update, aborting silently")
				return nil
			}

			if err := c.app.Update(); err != nil {
				c.Log().Debug().Str("Method", "Load").Int("Attempt", i+1).Err(err).Msg("app.Update retry")
				time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
				continue
			}
			app := c.app.App()
			if app != nil && app.TransportId != "" {
				transportId = app.TransportId
				c.Log().Debug().Str("Method", "Load").Str("TransportId", transportId).Msg("got transport ID")
				break
			}
			time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
		}

		if transportId == "" {
			lastErr = fmt.Errorf("failed to get transport ID after retries")
			if attempt < 5 {
				c.Log().Debug().Str("Method", "Load").Int("Attempt", attempt).Msg("no transport ID, TV may be waking up, retrying...")
				if !c.IsConnected() {
					c.Log().Debug().Str("Method", "Load").Msg("connection closed during retry wait, aborting silently")
					return nil
				}
				time.Sleep(4 * time.Second)
				continue
			}
			c.Log().Error().Str("Method", "Load").Msg("failed to get transport ID")
			return lastErr
		}

		// For live streams: load PAUSED then immediately send PLAY command
		// This simulates a "fast click" which avoids the 20-30s buffer that autoplay=true triggers
		autoplay := !live // Only autoplay if NOT a live stream
		err := LoadWithSubtitles(c.conn, transportId, mediaURL, contentType, startTime, duration, subtitleURL, live, autoplay)
		if err != nil {
			lastErr = err
			if isTimeoutError(err) && attempt < 5 {
				c.Log().Debug().Str("Method", "Load").Int("Attempt", attempt).Err(err).Msg("timeout, TV may be waking up, retrying...")
				if !c.IsConnected() {
					c.Log().Debug().Str("Method", "Load").Msg("connection closed during retry wait, aborting silently")
					return nil
				}
				time.Sleep(4 * time.Second)
				continue
			}
			c.Log().Error().Str("Method", "Load").Err(err).Msg("LoadWithSubtitles failed")
			return err
		}

		// For live streams: immediately send PLAY command after loading paused
		// This "fast play" behavior avoids the aggressive buffering that autoplay=true causes
		if live {
			c.Log().Debug().Str("Method", "Load").Msg("live stream loaded paused, sending immediate PLAY to simulate fast click")
			var playErr error
			for i := range 3 {
				// Refresh app state to get the mediaSessionId from LOAD response.
				// app.Unpause() needs this.
				if err := c.app.Update(); err != nil {
					playErr = err
					time.Sleep(time.Duration(i+1) * 200 * time.Millisecond)
					continue
				}

				// Use app.Unpause() instead of standalone Play() because:
				// 1. PLAY requires mediaSessionId from LOAD response.
				// 2. app.Unpause() uses that stored session id.
				playErr = c.app.Unpause()
				if playErr == nil {
					c.Log().Debug().Str("Method", "Load").Int("Attempt", i+1).Msg("play command sent successfully")
					break
				}
				time.Sleep(time.Duration(i+1) * 200 * time.Millisecond)
			}
			if playErr != nil {
				c.Log().Warn().Str("Method", "Load").Err(playErr).Msg("play command failed after retries")
			}
		}

		c.Log().Debug().Str("Method", "Load").Msg("load with subtitles/duration success")
		return nil
	}
	return lastErr
}

// LoadOnExisting loads media on an already-running receiver (for seek operations).
// Unlike Load, this skips launching the receiver.
// Use this when the receiver is already playing media and you want to load new content.
// If live is true, uses StreamType "LIVE" to identify as live stream.
func (c *CastClient) LoadOnExisting(mediaURL string, contentType string, startTime int, duration float64, subtitleURL string, live bool) error {
	c.Log().Debug().Str("Method", "LoadOnExisting").Str("URL", mediaURL).Str("ContentType", contentType).Int("StartTime", startTime).Float64("Duration", duration).Bool("HasSubs", subtitleURL != "").Bool("Live", live).Msg("loading media on existing receiver")

	// LoadOnExisting requires an active connection (it's designed for already-running receivers)
	// Unlike Load(), we don't auto-reconnect because that would defeat the optimization purpose
	if !c.IsConnected() {
		return fmt.Errorf("not connected (LoadOnExisting requires active connection)")
	}

	// Retry getting app state with backoff (handles transient errors during seek)
	var transportId string
	for i := range 5 {
		if !c.IsConnected() {
			c.Log().Debug().Str("Method", "LoadOnExisting").Msg("connection closed during app update, aborting silently")
			return nil
		}

		if err := c.app.Update(); err != nil {
			c.Log().Debug().Str("Method", "LoadOnExisting").Int("Attempt", i+1).Err(err).Msg("app.Update retry")
			time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
			continue
		}
		app := c.app.App()
		if app != nil && app.TransportId != "" {
			transportId = app.TransportId
			c.Log().Debug().Str("Method", "LoadOnExisting").Str("TransportId", transportId).Msg("got transport ID")
			break
		}
		time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
	}

	// For LoadOnExisting, always autoplay since it's for seek operations on active content
	err := LoadWithSubtitles(c.conn, transportId, mediaURL, contentType, startTime, duration, subtitleURL, live, true)
	if err != nil {
		c.Log().Error().Str("Method", "LoadOnExisting").Err(err).Msg("failed")
	} else {
		c.Log().Debug().Str("Method", "LoadOnExisting").Msg("success")
	}
	return err
}

// Play resumes playback.
func (c *CastClient) Play() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Log().Debug().Str("Method", "Play").Msg("resuming playback")
	err := c.app.Unpause()
	if err != nil {
		c.Log().Error().Str("Method", "Play").Err(err).Msg("failed")
	}
	return err
}

// Pause pauses playback.
func (c *CastClient) Pause() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Log().Debug().Str("Method", "Pause").Msg("pausing playback")
	err := c.app.Pause()
	if err != nil {
		c.Log().Error().Str("Method", "Pause").Err(err).Msg("failed")
	}
	return err
}

// Stop stops playback and closes the media session.
func (c *CastClient) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Log().Debug().Str("Method", "Stop").Msg("stopping playback")
	err := c.app.Stop()
	if err != nil {
		c.Log().Error().Str("Method", "Stop").Err(err).Msg("failed")
	}
	return err
}

// Seek seeks to position in seconds from start.
func (c *CastClient) Seek(seconds int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Log().Debug().Str("Method", "Seek").Int("Seconds", seconds).Msg("seeking")
	err := c.app.SeekFromStart(seconds)
	if err != nil {
		c.Log().Error().Str("Method", "Seek").Err(err).Msg("failed")
	}
	return err
}

// SetVolume sets volume (0.0 to 1.0).
func (c *CastClient) SetVolume(level float32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Log().Debug().Str("Method", "SetVolume").Float32("Level", level).Msg("setting volume")
	err := c.app.SetVolume(level)
	if err != nil {
		c.Log().Error().Str("Method", "SetVolume").Err(err).Msg("failed")
	}
	return err
}

// SetMuted sets mute state.
func (c *CastClient) SetMuted(muted bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Log().Debug().Str("Method", "SetMuted").Bool("Muted", muted).Msg("setting mute")
	err := c.app.SetMuted(muted)
	if err != nil {
		c.Log().Error().Str("Method", "SetMuted").Err(err).Msg("failed")
	}
	return err
}

// GetStatus returns current playback status.
// No mutex needed - only reads from underlying library which has its own sync.
func (c *CastClient) GetStatus() (*CastStatus, error) {
	// Request fresh status from device (Update refreshes the cached status)
	if err := c.app.Update(); err != nil {
		c.Log().Error().Str("Method", "GetStatus").Err(err).Msg("app.Update failed")
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

	c.Log().Debug().Str("Method", "Close").Bool("StopMedia", stopMedia).Msg("closing connection")
	c.connected = false
	err := c.app.Close(stopMedia)
	if err != nil {
		c.Log().Error().Str("Method", "Close").Err(err).Msg("failed")
	}
	return err
}

// IsConnected returns whether client is connected.
// Uses RLock for read-only access to avoid blocking on mutex contention.
func (c *CastClient) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// Host returns the hostname of the connected Chromecast device.
func (c *CastClient) Host() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.host
}

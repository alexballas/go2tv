package castprotocol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go2tv.app/go2tv/v2/castprotocol/v2/application"
	"go2tv.app/go2tv/v2/castprotocol/v2/cast"
	"go2tv.app/go2tv/v2/internal/logging"
	"go2tv.app/go2tv/v2/metadata"
)

// CastClient wraps go-chromecast Application for simplified API
type CastClient struct {
	app         *application.Application
	conn        cast.Conn // keep reference to connection for custom commands
	mu          sync.RWMutex
	host        string
	port        int
	connected   bool
	Logger      *slog.Logger
	LogOutput   io.Writer
	initLogOnce sync.Once
}

// Log returns the slog logger, initializing it lazily if LogOutput is set.
// Same pattern as TVPayload.Log() in soapcalls/soapcallers.go.
func (c *CastClient) Log() *slog.Logger {
	if c.LogOutput != nil {
		c.initLogOnce.Do(func() {
			c.Logger = logging.NewJSON(c.LogOutput)
		})
	}
	if c.Logger == nil {
		return logging.Discard
	}
	return c.Logger
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

	c.Log().Debug("connecting", "Method", "Connect", "Host", c.host, "Port", c.port)
	if err := c.app.Start(c.host, c.port); err != nil {
		c.Log().Error("connection failed", "Method", "Connect", "error", err)
		return fmt.Errorf("chromecast connect: %w", err)
	}
	c.connected = true
	c.Log().Debug("connected successfully", "Method", "Connect")
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

func (c *CastClient) defaultReceiverReady() bool {
	app := c.app.App()
	return app != nil && app.AppId == cast.DefaultMediaReceiverAppID && app.TransportId != ""
}

func normalizeMediaTitle(title string, mediaURL string) string {
	if normalized := deriveMediaTitle(title); normalized != "" {
		return normalized
	}
	return deriveMediaTitle(mediaURL)
}

func deriveMediaTitle(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	if u, err := url.Parse(value); err == nil && u.Scheme != "" {
		if base := path.Base(u.Path); base != "" && base != "." && base != "/" {
			return base
		}
		if host := strings.TrimSpace(u.Host); host != "" {
			return host
		}
	}

	if base := filepath.Base(value); base != "" && base != "." && base != string(filepath.Separator) {
		return base
	}

	return value
}

// Ensure the Default Media Receiver is running before custom media commands.
func (c *CastClient) ensureDefaultReceiverReady() error {
	if c.defaultReceiverReady() {
		return nil
	}

	if err := c.app.Update(); err == nil && c.defaultReceiverReady() {
		return nil
	}

	var lastErr error
	for attempt := range 5 {
		if !c.IsConnected() {
			c.Log().Debug("connection closed during receiver launch, aborting silently", "Method", "ensureDefaultReceiverReady")
			return nil
		}

		c.Log().Debug("launching default receiver", "Method", "ensureDefaultReceiverReady", "Attempt", attempt+1)
		if err := LaunchDefaultReceiver(c.conn); err != nil {
			lastErr = err
			if isTimeoutError(err) && attempt < 4 {
				c.Log().Debug("timeout, TV may be waking up, retrying...", "Method", "ensureDefaultReceiverReady", "Attempt", attempt+1, "error", err)
				if !c.IsConnected() {
					c.Log().Debug("connection closed during retry wait, aborting silently", "Method", "ensureDefaultReceiverReady")
					return nil
				}
				time.Sleep(4 * time.Second)
				continue
			}
			c.Log().Error("launch receiver failed", "Method", "ensureDefaultReceiverReady", "error", err)
			return fmt.Errorf("launch receiver: %w", err)
		}

		for i := range 8 {
			if !c.IsConnected() {
				c.Log().Debug("connection closed during app update, aborting silently", "Method", "ensureDefaultReceiverReady")
				return nil
			}

			if err := c.app.Update(); err != nil {
				lastErr = err
				c.Log().Debug("app.Update retry", "Method", "ensureDefaultReceiverReady", "Attempt", i+1, "error", err)
				time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
				continue
			}

			if c.defaultReceiverReady() {
				c.Log().Debug("default receiver ready", "Method", "ensureDefaultReceiverReady", "TransportId", c.app.App().TransportId)
				return nil
			}

			lastErr = fmt.Errorf("failed to get default receiver transport ID after retries")
			time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
		}

		if attempt < 4 {
			c.Log().Debug("receiver not ready, retrying...", "Method", "ensureDefaultReceiverReady", "Attempt", attempt+1)
			if !c.IsConnected() {
				c.Log().Debug("connection closed during retry wait, aborting silently", "Method", "ensureDefaultReceiverReady")
				return nil
			}
			time.Sleep(4 * time.Second)
			continue
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("failed to get default receiver transport ID after retries")
	}

	c.Log().Error("failed to launch default receiver", "Method", "ensureDefaultReceiverReady", "error", lastErr)
	return lastErr
}

// Load loads media from URL onto the Chromecast.
// startTime is the position in seconds to start playback from.
// duration is the total media duration in seconds (0 to let Chromecast detect).
// If subtitleURL is provided, uses custom load command with subtitle tracks.
// If live is true, uses StreamType "LIVE" to identify as live stream.
func (c *CastClient) Load(mediaURL string, contentType string, title string, startTime int, duration float64, subtitleURL string, live bool) error {
	return c.LoadMedia(LoadRequest{
		MediaURL:    mediaURL,
		ContentType: contentType,
		Metadata:    metadata.Media{Title: title},
		StartTime:   startTime,
		Duration:    duration,
		SubtitleURL: subtitleURL,
		Live:        live,
	})
}

// LoadMedia loads media and protocol-neutral metadata onto the Chromecast.
func (c *CastClient) LoadMedia(req LoadRequest) error {
	req.Metadata.Title = normalizeMediaTitle(req.Metadata.Title, req.MediaURL)
	c.Log().Debug("loading media", "Method", "LoadMedia", "URL", req.MediaURL, "ContentType", req.ContentType, "Title", req.Metadata.Title, "StartTime", req.StartTime, "Duration", req.Duration, "HasSubs", req.SubtitleURL != "", "HasArtwork", req.Metadata.Artwork != nil, "Live", req.Live)

	// Check if connection is still active, reconnect if needed
	// This handles cases where Close() was called but the client is being reused
	if !c.IsConnected() {
		c.Log().Debug("connection closed, reconnecting", "Method", "Load")
		if err := c.Connect(); err != nil {
			return fmt.Errorf("reconnect before load: %w", err)
		}
	}

	// Metadata, subtitles, duration, and live streams require a custom LOAD.
	// (go-chromecast library hardcodes StreamType "BUFFERED" so we need custom path for LIVE)
	if !requiresCustomLoad(req) {
		// Retry loop for TV wake-up scenarios (timeout errors)
		var lastErr error
		for attempt := range 5 {
			if !c.IsConnected() {
				c.Log().Debug("connection closed during load, aborting silently", "Method", "Load")
				return nil
			}
			if err := c.app.Load(req.MediaURL, req.StartTime, req.ContentType, false, false, false); err != nil {
				lastErr = err
				if isTimeoutError(err) && attempt < 5 {
					c.Log().Debug("timeout, TV may be waking up, retrying...", "Method", "Load", "Attempt", attempt, "error", err)
					if !c.IsConnected() {
						c.Log().Debug("connection closed during retry wait, aborting silently", "Method", "Load")
						return nil
					}
					time.Sleep(4 * time.Second) // Wait for TV to wake up
					continue
				}
				c.Log().Error("standard load failed", "Method", "Load", "error", err)
				return err
			}
			c.Log().Debug("standard load success", "Method", "Load")
			return nil
		}
		return lastErr
	}

	// With subtitles or custom duration: launch the app first WITHOUT loading media, then send custom load
	// This prevents double playback (first without subs, then with subs queued)
	// Retry loop for TV wake-up scenarios
	if err := c.ensureDefaultReceiverReady(); err != nil {
		return err
	}

	var lastErr error
	for attempt := range 5 {
		if !c.IsConnected() {
			c.Log().Debug("connection closed during load, aborting silently", "Method", "Load")
			return nil
		}

		app := c.app.App()
		transportId := ""
		if app != nil {
			transportId = app.TransportId
		}

		if transportId == "" {
			lastErr = fmt.Errorf("failed to get transport ID after receiver launch")
			if attempt < 4 {
				c.Log().Debug("no transport ID, retrying...", "Method", "Load", "Attempt", attempt+1)
				if !c.IsConnected() {
					c.Log().Debug("connection closed during retry wait, aborting silently", "Method", "Load")
					return nil
				}
				time.Sleep(4 * time.Second)
				continue
			}
			c.Log().Error("failed to get transport ID", "Method", "Load", "error", lastErr)
			return lastErr
		}

		// For live streams: load PAUSED then immediately send PLAY command
		// This simulates a "fast click" which avoids the 20-30s buffer that autoplay=true triggers
		autoplay := !req.Live // Only autoplay if NOT a live stream
		err := loadMedia(c.conn, transportId, req, autoplay)
		if err != nil {
			lastErr = err
			if isTimeoutError(err) && attempt < 5 {
				c.Log().Debug("timeout, TV may be waking up, retrying...", "Method", "Load", "Attempt", attempt, "error", err)
				if !c.IsConnected() {
					c.Log().Debug("connection closed during retry wait, aborting silently", "Method", "Load")
					return nil
				}
				time.Sleep(4 * time.Second)
				continue
			}
			c.Log().Error("custom LOAD failed", "Method", "LoadMedia", "error", err)
			return err
		}

		// For live streams: immediately send PLAY command after loading paused
		// This "fast play" behavior avoids the aggressive buffering that autoplay=true causes
		if req.Live {
			c.Log().Debug("live stream loaded paused, sending immediate PLAY to simulate fast click", "Method", "Load")
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
					c.Log().Debug("play command sent successfully", "Method", "Load", "Attempt", i+1)
					break
				}
				time.Sleep(time.Duration(i+1) * 200 * time.Millisecond)
			}
			if playErr != nil {
				c.Log().Warn("play command failed after retries", "Method", "Load", "error", playErr)
			}
		}

		c.Log().Debug("custom LOAD success", "Method", "LoadMedia")
		return nil
	}
	return lastErr
}

func requiresCustomLoad(req LoadRequest) bool {
	return req.SubtitleURL != "" || req.Duration != 0 || hasMediaMetadata(req.Metadata) || req.Live
}

// LoadOnExisting loads media on an already-running receiver (for seek operations).
// Unlike Load, this skips launching the receiver.
// Use this when the receiver is already playing media and you want to load new content.
// If live is true, uses StreamType "LIVE" to identify as live stream.
func (c *CastClient) LoadOnExisting(mediaURL string, contentType string, title string, startTime int, duration float64, subtitleURL string, live bool) error {
	return c.LoadMediaOnExisting(LoadRequest{
		MediaURL:    mediaURL,
		ContentType: contentType,
		Metadata:    metadata.Media{Title: title},
		StartTime:   startTime,
		Duration:    duration,
		SubtitleURL: subtitleURL,
		Live:        live,
	})
}

// LoadMediaOnExisting loads media and metadata on an already-running receiver.
func (c *CastClient) LoadMediaOnExisting(req LoadRequest) error {
	req.Metadata.Title = normalizeMediaTitle(req.Metadata.Title, req.MediaURL)
	c.Log().Debug("loading media on existing receiver", "Method", "LoadMediaOnExisting", "URL", req.MediaURL, "ContentType", req.ContentType, "Title", req.Metadata.Title, "StartTime", req.StartTime, "Duration", req.Duration, "HasSubs", req.SubtitleURL != "", "HasArtwork", req.Metadata.Artwork != nil, "Live", req.Live)

	// LoadOnExisting requires an active connection (it's designed for already-running receivers)
	// Unlike Load(), we don't auto-reconnect because that would defeat the optimization purpose
	if !c.IsConnected() {
		return fmt.Errorf("not connected (LoadOnExisting requires active connection)")
	}

	// Retry getting app state with backoff (handles transient errors during seek)
	var transportId string
	for i := range 5 {
		if !c.IsConnected() {
			c.Log().Debug("connection closed during app update, aborting silently", "Method", "LoadOnExisting")
			return nil
		}

		if err := c.app.Update(); err != nil {
			c.Log().Debug("app.Update retry", "Method", "LoadOnExisting", "Attempt", i+1, "error", err)
			time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
			continue
		}
		app := c.app.App()
		if app != nil && app.TransportId != "" {
			transportId = app.TransportId
			c.Log().Debug("got transport ID", "Method", "LoadOnExisting", "TransportId", transportId)
			break
		}
		time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
	}
	if transportId == "" {
		return fmt.Errorf("media receiver unavailable: missing transport ID")
	}

	// For LoadOnExisting, always autoplay since it's for seek operations on active content
	err := loadMedia(c.conn, transportId, req, true)
	if err != nil {
		c.Log().Error("failed", "Method", "LoadOnExisting", "error", err)
	} else {
		c.Log().Debug("success", "Method", "LoadOnExisting")
	}
	return err
}

// Play resumes playback.
func (c *CastClient) Play() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Log().Debug("resuming playback", "Method", "Play")
	err := c.app.Unpause()
	if err != nil {
		c.Log().Error("failed", "Method", "Play", "error", err)
	}
	return err
}

// Pause pauses playback.
func (c *CastClient) Pause() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Log().Debug("pausing playback", "Method", "Pause")
	err := c.app.Pause()
	if err != nil {
		c.Log().Error("failed", "Method", "Pause", "error", err)
	}
	return err
}

// Stop stops playback and closes the media session.
func (c *CastClient) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Log().Debug("stopping playback", "Method", "Stop")
	err := c.app.Stop()
	if err != nil {
		c.Log().Error("failed", "Method", "Stop", "error", err)
	}
	return err
}

// Seek seeks to position in seconds from start.
func (c *CastClient) Seek(seconds int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Log().Debug("seeking", "Method", "Seek", "Seconds", seconds)
	err := c.app.SeekFromStart(seconds)
	if err != nil {
		c.Log().Error("failed", "Method", "Seek", "error", err)
	}
	return err
}

// SetVolume sets volume (0.0 to 1.0).
func (c *CastClient) SetVolume(level float32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Log().Debug("setting volume", "Method", "SetVolume", "Level", level)
	err := c.app.SetVolume(level)
	if err != nil {
		c.Log().Error("failed", "Method", "SetVolume", "error", err)
	}
	return err
}

// SetMuted sets mute state.
func (c *CastClient) SetMuted(muted bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Log().Debug("setting mute", "Method", "SetMuted", "Muted", muted)
	err := c.app.SetMuted(muted)
	if err != nil {
		c.Log().Error("failed", "Method", "SetMuted", "error", err)
	}
	return err
}

// GetStatus returns current playback status.
// No mutex needed - only reads from underlying library which has its own sync.
func (c *CastClient) GetStatus() (*CastStatus, error) {
	// Single attempt on purpose: status callers poll frequently and tolerate
	// misses, so a fast error beats blocking ~35s in the wake-up retry loop
	// (that loop stays on the Load/Connect paths via app.Update).
	if err := c.app.UpdateOnce(); err != nil {
		c.Log().Error("app.Update failed", "Method", "GetStatus", "error", err)
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

	c.Log().Debug("closing connection", "Method", "Close", "StopMedia", stopMedia)
	c.connected = false
	err := c.app.Close(stopMedia)
	if err != nil {
		c.Log().Error("failed", "Method", "Close", "error", err)
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

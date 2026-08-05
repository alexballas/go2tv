package playback

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrSeekNegative     = errors.New("seek position negative")
	ErrSeekPastDuration = errors.New("seek position past duration")
	ErrSeekUnsupported  = errors.New("seek protocol unsupported")
	ErrStaleOperation   = errors.New("stale playback operation")
)

type SeekRequest struct {
	Protocol   string
	Transcoded bool
	Seconds    int
	Duration   int
	Server     ServerRequest
	Load       LoadRequest
}

// SeekEngine owns operation generations. Transport calls remain synchronous and cancellable.
type SeekEngine struct {
	mu         sync.Mutex
	generation uint64
	dlna       DLNATransport
	cast       ChromecastTransport
	server     MediaServer
}

func NewSeekEngine(dlna DLNATransport, cast ChromecastTransport, server MediaServer) *SeekEngine {
	return &SeekEngine{dlna: dlna, cast: cast, server: server}
}

func (e *SeekEngine) Generation() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.generation
}

func (e *SeekEngine) nextGeneration() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.generation++
	return e.generation
}

func (e *SeekEngine) current(g uint64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.generation == g
}

func ValidateSeek(seconds, duration int) error {
	if seconds < 0 {
		return ErrSeekNegative
	}
	if duration < 0 || seconds > duration {
		return ErrSeekPastDuration
	}
	return nil
}

func (e *SeekEngine) Seek(ctx context.Context, req SeekRequest) (uint64, error) {
	if err := ValidateSeek(req.Seconds, req.Duration); err != nil {
		return e.Generation(), err
	}
	if ctx == nil {
		return e.Generation(), errors.New("seek context required")
	}
	generation := e.nextGeneration()
	var err error
	switch req.Protocol {
	case "DLNA":
		if e.dlna == nil {
			err = ErrSeekUnsupported
			break
		}
		if !req.Transcoded {
			err = e.dlna.Seek(ctx, clockTime(req.Seconds))
			break
		}
		err = e.restartDLNA(ctx, req)
	case "Chromecast":
		if e.cast == nil {
			err = ErrSeekUnsupported
			break
		}
		if !req.Transcoded {
			err = e.cast.Seek(ctx, req.Seconds)
			break
		}
		err = e.restartCast(ctx, req)
	default:
		err = ErrSeekUnsupported
	}
	if err != nil {
		return generation, err
	}
	if !e.current(generation) {
		return generation, ErrStaleOperation
	}
	return generation, nil
}

func (e *SeekEngine) restartDLNA(ctx context.Context, req SeekRequest) error {
	if e.server == nil {
		return errors.New("media server unavailable")
	}
	if err := e.dlna.Stop(ctx); err != nil {
		return fmt.Errorf("stop DLNA: %w", err)
	}
	if err := e.server.Stop(ctx); err != nil {
		return fmt.Errorf("stop media server: %w", err)
	}
	req.Server.SeekOffset = req.Seconds
	route, err := e.server.Start(ctx, req.Server)
	if err != nil {
		return fmt.Errorf("restart media server: %w", err)
	}
	req.Load.MediaURL, req.Load.Start = route.URL, 0
	e.restoreArtwork(ctx, &req.Load)
	if err := e.dlna.Load(ctx, req.Load); err != nil {
		return fmt.Errorf("reload DLNA: %w", err)
	}
	if err := e.dlna.Play(ctx); err != nil {
		return fmt.Errorf("replay DLNA: %w", err)
	}
	return nil
}

func (e *SeekEngine) restartCast(ctx context.Context, req SeekRequest) error {
	if e.server == nil {
		return errors.New("media server unavailable")
	}
	if err := e.server.Stop(ctx); err != nil {
		return fmt.Errorf("stop media server: %w", err)
	}
	req.Server.SeekOffset = req.Seconds
	route, err := e.server.Start(ctx, req.Server)
	if err != nil {
		return fmt.Errorf("restart media server: %w", err)
	}
	req.Load.MediaURL, req.Load.Start = route.URL, 0
	e.restoreArtwork(ctx, &req.Load)
	if err := e.cast.LoadOnExisting(ctx, req.Load); err != nil {
		return fmt.Errorf("reload Chromecast: %w", err)
	}
	return nil
}

func (e *SeekEngine) restoreArtwork(ctx context.Context, load *LoadRequest) {
	if e.server == nil || load == nil || len(load.ArtworkData) == 0 || load.Metadata.Artwork == nil {
		return
	}
	route, err := e.server.Add(ctx, RouteRequest{MediaType: load.Metadata.Artwork.MIMEType, Contents: load.ArtworkData})
	if err != nil {
		load.Metadata.Artwork = nil
		return
	}
	load.Metadata.Artwork.URL = route.URL
}

func clockTime(seconds int) string {
	h := seconds / 3600
	m := seconds % 3600 / 60
	s := seconds % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

//go:build !(android || ios)

package gui

import (
	"errors"
	"io"
	"sync"

	"go2tv.app/screencast/hls"
	"go2tv.app/screencast/ts"
)

// dlnaScreencastMediaType is the DLNA media type used for the live MPEG-TS
// stream. The first path segment ("video") matches the Sink protocol list
// advertised by most renderers (see parseProtocolInfo, which compares only
// that segment). The pipeline itself is the shared go2tv.app/screencast/ts
// package, which muxes plain 188-byte TS packets, so this pairs with the _ISO
// profile in dlnaprofiles rather than the 192-byte m2ts one.
//
// video/vnd.dlna.mpeg-tts, the type DLNA pairs with the m2ts profiles, is
// deliberately not used: ffmpeg's m2ts mode declares the AAC track as PES
// private data, which GStreamer-based renderers drop, leaving a silent
// picture. See the screencast/ts package comment.
const dlnaScreencastMediaType = "video/mp2t"

// errScreencastStreamBusy is returned when the live stream is already being
// served to a renderer.
var errScreencastStreamBusy = errors.New("screencast stream already has a reader")

// startDLNAScreencast starts the shared MPEG-TS pipeline. *ts.Session already
// satisfies screencastSession, so it is handed to the caller as is.
//
// Audio is always captured. It is not a preference: we advertise the
// AVC_TS_MP_HD_AAC_MULT5_ISO profile, which promises the renderer an AAC track,
// so a video-only stream would make that promise false. Nothing is lost by
// forcing it either, because the pipeline substitutes synthetic silence when
// the machine has no audio source, so an AAC track exists either way.
func startDLNAScreencast(ffmpegPath string, logOutput io.Writer) (*ts.Session, error) {
	return ts.Start(&ts.Options{
		FFmpegPath:   ffmpegPath,
		IncludeAudio: true,
		LogOutput:    logOutput,
		DebugCommand: hls.BoolEnv("GO2TV_FFMPEG_DEBUG", false),
	})
}

// screencastStream lends the live MPEG-TS reader to one HTTP request at a
// time. There is a single reader behind it - ffmpeg's stdout - so two requests
// reading at once would each get an arbitrary half of the packets and corrupt
// both streams. Handing the second request an error instead lets the server
// answer it with 503, leaving the renderer that got there first playing.
//
// Sequential reconnects are unaffected: the lease is released when the request
// ends, and ffmpeg repeats the PAT and PMT often enough for a renderer to join
// mid-stream.
type screencastStream struct {
	stream io.ReadCloser
	mu     sync.Mutex
	held   bool
}

// acquire hands out the reader, or errScreencastStreamBusy if a request
// already holds it.
func (s *screencastStream) acquire() (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.held {
		return nil, errScreencastStreamBusy
	}

	s.held = true
	return &screencastLease{parent: s}, nil
}

func (s *screencastStream) release() {
	s.mu.Lock()
	s.held = false
	s.mu.Unlock()
}

// screencastLease is one request's borrow of the live stream. Closing it
// returns the lease without touching the underlying reader: the ts.Session
// owns the ffmpeg process, and tearing it down on a renderer disconnect would
// end the screencast instead of letting the next request pick it up.
type screencastLease struct {
	parent *screencastStream
	once   sync.Once
}

func (l *screencastLease) Read(p []byte) (int, error) {
	return l.parent.stream.Read(p)
}

func (l *screencastLease) Close() error {
	l.once.Do(l.parent.release)
	return nil
}

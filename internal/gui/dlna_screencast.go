//go:build !(android || ios)

package gui

import (
	"io"
	"os"
	"strconv"
	"strings"

	"go2tv.app/screencast/hls"
	"go2tv.app/screencast/ts"
)

// dlnaScreencastMediaType is the DLNA media type used for the live MPEG-TS
// stream. The first path segment ("video") matches the Sink protocol list
// advertised by most renderers (see parseProtocolInfo). The pipeline itself is
// the shared go2tv.app/screencast/ts package: it muxes to 192-byte m2ts
// packets, which is what this media type requires.
const dlnaScreencastMediaType = "video/vnd.dlna.mpeg-tts"

// screencastAudioEnv opts the DLNA screencast out of audio capture.
const screencastAudioEnv = "GO2TV_DLNA_SCREENCAST_AUDIO"

// dlnaScreencastSession is the screencastSession implementation backed by the
// shared go2tv.app/screencast/ts MPEG-TS pipeline.
type dlnaScreencastSession struct {
	session *ts.Session
}

// Stream returns the live MPEG-TS output.
func (s *dlnaScreencastSession) Stream() io.ReadCloser {
	if s == nil || s.session == nil {
		return nil
	}
	return s.session.Stream()
}

func (s *dlnaScreencastSession) Done() <-chan error {
	if s == nil || s.session == nil {
		return nil
	}
	return s.session.Done()
}

func (s *dlnaScreencastSession) StderrTail(n int) string {
	if s == nil || s.session == nil {
		return ""
	}
	return s.session.StderrTail(n)
}

func (s *dlnaScreencastSession) Close() error {
	if s == nil || s.session == nil {
		return nil
	}
	return s.session.Close()
}

// screencastAudioEnabled reports whether the DLNA screencast captures audio.
// Audio is on by default: the media type advertises the
// AVC_TS_MP_HD_AAC_MULT5 profile, which promises an AAC track. A value that
// does not parse leaves the default alone rather than silently muting the
// stream.
func screencastAudioEnabled(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}

	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return true
	}

	return enabled
}

func startDLNAScreencast(ffmpegPath string, logOutput io.Writer) (*dlnaScreencastSession, error) {
	session, err := ts.Start(&ts.Options{
		FFmpegPath:   ffmpegPath,
		IncludeAudio: screencastAudioEnabled(os.Getenv(screencastAudioEnv)),
		LogOutput:    logOutput,
		DebugCommand: hls.BoolEnv("GO2TV_FFMPEG_DEBUG", false),
	})
	if err != nil {
		return nil, err
	}

	return &dlnaScreencastSession{session: session}, nil
}

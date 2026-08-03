//go:build !(android || ios)

package gui

import (
	"io"
	"os"
	"strconv"
	"strings"

	"go2tv.app/screencast/dlna"
	"go2tv.app/screencast/hls"
)

// dlnaScreencastMediaType is the DLNA media type used for the live MPEG-TS
// stream. The first path segment ("video") matches the Sink protocol list
// advertised by most renderers (see parseProtocolInfo). The pipeline itself is
// the shared go2tv.app/screencast/dlna package: it muxes to 192-byte m2ts
// packets, which is what this media type requires.
const dlnaScreencastMediaType = "video/vnd.dlna.mpeg-tts"

// dlnaScreencastSession is the screencastSession implementation backed by the
// shared go2tv.app/screencast/dlna MPEG-TS pipeline.
type dlnaScreencastSession struct {
	session *dlna.Session
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

func startDLNAScreencast(ffmpegPath string, logOutput io.Writer) (*dlnaScreencastSession, error) {
	includeAudio := true
	if v := strings.TrimSpace(os.Getenv("GO2TV_DLNA_SCREENCAST_AUDIO")); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			includeAudio = b
		}
	}

	session, err := dlna.Start(&dlna.Options{
		FFmpegPath:   ffmpegPath,
		IncludeAudio: includeAudio,
		LogOutput:    logOutput,
		DebugCommand: hls.BoolEnv("GO2TV_FFMPEG_DEBUG", false),
	})
	if err != nil {
		return nil, err
	}
	return &dlnaScreencastSession{session: session}, nil
}

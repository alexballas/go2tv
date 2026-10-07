package utils

import (
	"io"
	"log/slog"
	"sync"

	"go2tv.app/go2tv/v2/internal/logging"
	"go2tv.app/go2tv/v2/internal/mediasource"
)

// TranscodeOptions holds shared FFmpeg transcoding configuration for both
// protocols. DLNA's legacy TVPayload is adapted by the HTTP handler.
//
// Field descriptions:
//
//	FFmpegPath: Absolute path to the ffmpeg binary executable.
//	            Example: "/usr/bin/ffmpeg" or "C:\ffmpeg\bin\ffmpeg.exe"
//	            Used to spawn the ffmpeg process for transcoding.
//
//	SubsPath: Path to subtitle file to burn into the video stream.
//	          Supports SRT, VTT, ASS and SSA formats. When set, subtitles are
//	          embedded via ffmpeg's -vf subtitles filter.
//	          Empty string uses EmbeddedSubtitle or TorrentSource when provided.
//
//	SeekSeconds: Starting position in seconds for transcoding.
//	             Used with ffmpeg's -ss flag for seeking.
//	             Value of 0 starts from the beginning.
//	             Enables seek support during transcoded playback.
//
//	SubtitleSize: Font size for burned-in plain text subtitles.
//	              ASS/SSA uses the script's original styles and sizes.
//	              Use SubtitleSizeSmall (20), SubtitleSizeMedium (24),
//	              or SubtitleSizeLarge (30).
//
//	LogOutput: io.Writer for debug logging (same pattern as TVPayload).
//	           Pass screen.Debug to enable export from settings menu.
//	           Pass nil to disable logging.
//	           Includes full FFmpeg arguments for each transcode attempt.
type TranscodeOptions struct {
	FFmpegPath   string
	SubsPath     string
	SeekSeconds  int
	SubtitleSize SubtitleSize
	// SubtitleFontsDir retains adjacent ASS/SSA fonts when a subtitle source
	// is copied into a temporary file. Defaults to the subtitle's directory.
	SubtitleFontsDir string
	LogOutput        io.Writer
	RawInput         *RawVideoInput
	// EmbeddedSubtitle renders a selected local ASS/SSA or bitmap track directly
	// from its container, preserving typesetting and attached fonts. Used when
	// SubsPath is empty; an external subtitle selection takes priority.
	EmbeddedSubtitle *EmbeddedSubtitle
	// TorrentSource burns the first supported embedded text track when SubsPath
	// is empty. Callers select this only for automatic torrent captions.
	TorrentSource mediasource.Source

	initLogOnce sync.Once
	logger      *slog.Logger
}

type EmbeddedSubtitle struct {
	Path   string
	Track  int // Zero-based subtitle stream index, as in FFmpeg's 0:s:N selector.
	Bitmap bool
}

// RawVideoInput describes a raw video stream piped to ffmpeg stdin.
type RawVideoInput struct {
	Width       uint32
	Height      uint32
	FrameRate   uint32
	PixelFormat string
}

// LogError logs an error using the same pattern as TVPayload.Log().
// Does nothing if LogOutput is nil.
func (t *TranscodeOptions) LogError(function, action string, err error) {
	t.Log().Error("", "function", function, "Action", action, "error", err)
}

// Log returns the optional debug logger, or a discard logger when disabled.
func (t *TranscodeOptions) Log() *slog.Logger {
	if t.LogOutput == nil {
		return logging.Discard
	}
	t.initLogOnce.Do(func() {
		t.logger = logging.NewJSON(t.LogOutput)
	})
	return t.logger
}

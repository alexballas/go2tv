package utils

import (
	"io"
	"sync"

	"github.com/rs/zerolog"
)

// TranscodeOptions holds FFmpeg transcoding configuration for Chromecast.
// Used by StartSimpleServerWithTranscode() which doesn't use TVPayload.
//
// Field descriptions:
//
//	FFmpegPath: Absolute path to the ffmpeg binary executable.
//	            Example: "/usr/bin/ffmpeg" or "C:\ffmpeg\bin\ffmpeg.exe"
//	            Used to spawn the ffmpeg process for transcoding.
//
//	SubsPath: Path to subtitle file to burn into the video stream.
//	          Supports SRT and VTT formats. When set, subtitles are
//	          embedded via ffmpeg's -vf subtitles filter.
//	          Empty string means no subtitle burning.
//	          Only used when user explicitly selects subtitles.
//
//	SeekSeconds: Starting position in seconds for transcoding.
//	             Used with ffmpeg's -ss flag for seeking.
//	             Value of 0 starts from the beginning.
//	             Enables seek support during transcoded playback.
//
//	SubtitleSize: Font size for burned-in subtitles.
//	              Use SubtitleSizeSmall (20), SubtitleSizeMedium (24),
//	              or SubtitleSizeLarge (30). Ignored if SubsPath is empty.
//
//	LogOutput: io.Writer for debug logging (same pattern as TVPayload).
//	           Pass screen.Debug to enable export from settings menu.
//	           Pass nil to disable logging.
type TranscodeOptions struct {
	FFmpegPath   string
	SubsPath     string
	SeekSeconds  int
	SubtitleSize SubtitleSize
	LogOutput    io.Writer

	initLogOnce sync.Once
	logger      zerolog.Logger
}

// LogError logs an error using the same pattern as TVPayload.Log().
// Does nothing if LogOutput is nil.
func (t *TranscodeOptions) LogError(function, action string, err error) {
	if t.LogOutput == nil {
		return
	}
	t.initLogOnce.Do(func() {
		t.logger = zerolog.New(t.LogOutput).With().Timestamp().Logger()
	})
	t.logger.Error().Str("function", function).Str("Action", action).Err(err).Msg("")
}

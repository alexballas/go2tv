//go:build !(android || ios)

package servermode

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"go2tv.app/go2tv/v2/internal/controller"
	"go2tv.app/go2tv/v2/internal/library"
	"go2tv.app/go2tv/v2/internal/mediaserver"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/internal/playbackadapter"
	"go2tv.app/go2tv/v2/internal/webui"
	"go2tv.app/go2tv/v2/utils"
)

type runtime struct {
	library    *library.Library
	controller *controller.Controller
	callbacks  *playbackadapter.CallbackBridge
	web        *webui.Handler
}

func newRuntime(cfg Config, log *serverLogger) (*runtime, error) {
	lib, err := library.Open(library.Config{Roots: cfg.MediaRoots})
	if err != nil {
		return nil, err
	}
	callbacks := playbackadapter.NewCallbackBridge()
	base := mediaserver.New(mediaserver.Config{Callback: callbacks, Transcode: transcode})
	media := &runtimeMediaServer{Server: base}
	artwork := controller.NewArtworkCache(controller.ArtworkCacheBytes)
	ffmpeg, _ := utils.ResolveFFmpegPath("")
	var durationProbe func(context.Context, playback.SourceOpener) (float64, error)
	if ffmpeg != "" {
		durationProbe = func(ctx context.Context, open playback.SourceOpener) (float64, error) {
			media, _, err := open(ctx)
			if err != nil {
				return 0, err
			}
			defer media.Close()
			return utils.DurationForMediaReaderSeconds(ctx, ffmpeg, media)
		}
	}
	control := controller.New(controller.NewRuntimeConfig(controller.RuntimeConfig{MediaServer: media, Callbacks: callbacks, LogOutput: log.protocolOutput(), Logger: log, Artwork: artwork, DurationProbe: durationProbe}))
	web, err := webui.New(webui.Config{Version: cfg.Version, Controller: control, Library: lib, Artwork: artwork, FFmpegPath: ffmpeg, Logger: log})
	if err != nil {
		control.Close()
		callbacks.Close()
		_ = lib.Close()
		return nil, err
	}
	return &runtime{library: lib, controller: control, callbacks: callbacks, web: web}, nil
}

func (r *runtime) Close() {
	r.web.Close()
	r.controller.Close()
	r.callbacks.Close()
	_ = r.library.Close()
}

type runtimeMediaServer struct{ *mediaserver.Server }

func (s *runtimeMediaServer) Start(ctx context.Context, request playback.ServerRequest) (playback.MediaRoute, error) {
	request = prepareServerRequest(request)
	if request.Subtitle != nil && request.Transcode {
		request.BurnSubtitle = true
	}
	if request.Subtitle != nil && request.Target.Protocol == "Chromecast" && !request.Transcode && filepath.Ext(request.SubtitleExt) == ".srt" {
		original := request.Subtitle
		request.Subtitle = func(ctx context.Context) (io.ReadSeekCloser, time.Time, error) {
			source, mod, err := original(ctx)
			if err != nil {
				return nil, time.Time{}, err
			}
			defer source.Close()
			converted, err := utils.ConvertSRTReaderToWebVTT(source)
			if err != nil {
				return nil, time.Time{}, err
			}
			return &memoryFile{Reader: *bytes.NewReader(converted)}, mod, nil
		}
		request.SubtitleExt = ".vtt"
	}
	return s.Server.Start(ctx, request)
}

func prepareServerRequest(request playback.ServerRequest) playback.ServerRequest {
	if request.Transcode && isChromecastRequest(request) {
		request.MediaExt = ".mp4"
		request.MediaType = "video/mp4"
	}
	return request
}

func isChromecastRequest(request playback.ServerRequest) bool {
	return request.Target.Protocol == "Chromecast"
}

type memoryFile struct{ bytes.Reader }

func (*memoryFile) Close() error { return nil }

func transcode(ctx context.Context, w http.ResponseWriter, input io.ReadCloser, request playback.ServerRequest) error {
	ffmpeg, err := utils.ResolveFFmpegPath("")
	if err != nil {
		return err
	}
	subtitlePath := ""
	if request.BurnSubtitle && request.Subtitle != nil {
		subtitle, _, openErr := request.Subtitle(ctx)
		if openErr != nil {
			return openErr
		}
		defer subtitle.Close()
		temp, createErr := os.CreateTemp("", "go2tv-subtitle-*.srt")
		if createErr != nil {
			return createErr
		}
		subtitlePath = temp.Name()
		defer os.Remove(subtitlePath)
		if _, err = io.Copy(temp, subtitle); err == nil {
			err = temp.Close()
		} else {
			_ = temp.Close()
		}
		if err != nil {
			return err
		}
	}
	var command exec.Cmd
	if isChromecastRequest(request) {
		return utils.ServeChromecastTranscodedStream(ctx, w, input, &command, &utils.TranscodeOptions{
			FFmpegPath:   ffmpeg,
			SubsPath:     subtitlePath,
			SeekSeconds:  request.SeekOffset,
			SubtitleSize: utils.SubtitleSizeMedium,
		})
	}
	return utils.ServeTranscodedStream(ctx, w, input, &command, ffmpeg, subtitlePath, request.SeekOffset, utils.SubtitleSizeMedium)
}

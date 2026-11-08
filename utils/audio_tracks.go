package utils

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ErrNoAudioTracks reports media without selectable audio streams.
var ErrNoAudioTracks = errors.New("no audio tracks")

// AudioTrack describes one source audio stream. Number is one-based and
// matches TranscodeOptions.AudioTrack (FFmpeg 0:a:Number-1).
type AudioTrack struct {
	Number   int
	Language string
	Title    string
	Codec    string
	Channels int
	Default  bool
}

type ffprobeAudioStreams struct {
	Streams []struct {
		CodecName   string `json:"codec_name"`
		Channels    int    `json:"channels"`
		Disposition struct {
			Default int `json:"default"`
		} `json:"disposition"`
		Tags struct {
			Language string `json:"language"`
			Title    string `json:"title"`
		} `json:"tags"`
	} `json:"streams"`
}

// AudioTracksContext lists the audio streams of a local path or progressive source.
func AudioTracksContext(ctx context.Context, ffmpeg, f string) ([]AudioTrack, error) {
	if ctx == nil {
		return nil, errors.New("AudioTracksContext: context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Check FFmpeg first: resolving a torrent source may start downloading.
	if err := CheckFFmpegContext(ctx, ffmpeg); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("AudioTracksContext: %w: %w", ErrFFmpegUnavailable, err)
	}
	f, err := mediaInput(f)
	if err != nil {
		return nil, fmt.Errorf("AudioTracksContext: %w", err)
	}
	return probeAudioTracks(ctx, ffmpeg, f, nil)
}

// AudioTracksReaderContext lists audio streams without a filesystem path,
// e.g. Android content:// descriptors. The reader offset is restored.
func AudioTracksReaderContext(ctx context.Context, ffmpeg string, media io.ReadSeekCloser) (tracks []AudioTrack, err error) {
	if ctx == nil {
		return nil, errors.New("AudioTracksReaderContext: context required")
	}
	if media == nil {
		return nil, ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input := io.Reader(media)
	inputURL := "pipe:0"
	if url := progressiveReaderURL(media); url != "" {
		input, inputURL = nil, url
	} else if file, ok := underlyingOSFile(media); ok {
		input = file
		inputURL = ffmpegInputForFileContext(ctx, ffmpeg, file)
	}
	if seeker, ok := input.(io.Seeker); ok {
		offset, seekErr := seeker.Seek(0, io.SeekCurrent)
		if seekErr == nil {
			if _, seekErr = seeker.Seek(0, io.SeekStart); seekErr != nil {
				return nil, fmt.Errorf("AudioTracksReaderContext: rewind: %w", seekErr)
			}
			defer func() {
				if _, restoreErr := seeker.Seek(offset, io.SeekStart); err == nil && restoreErr != nil {
					err = fmt.Errorf("AudioTracksReaderContext: restore: %w", restoreErr)
				}
			}()
		}
	}
	return probeAudioTracks(ctx, ffmpeg, inputURL, input)
}

func probeAudioTracks(ctx context.Context, ffmpeg, in string, stdin io.Reader) ([]AudioTrack, error) {
	ffprobe, err := ResolveFFprobePath(ffmpeg)
	if err != nil {
		return nil, fmt.Errorf("probeAudioTracks: %w: %w", ErrFFmpegUnavailable, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffprobe, "-v", "error", "-select_streams", "a",
		"-show_entries", "stream=codec_name,channels:stream_disposition=default:stream_tags=language,title",
		"-of", "json", in)
	setSysProcAttr(cmd)
	cmd.WaitDelay = time.Second
	cmd.Stdin = stdin
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("probeAudioTracks: %w", err)
	}
	return parseAudioTracks(output)
}

func parseAudioTracks(output []byte) ([]AudioTrack, error) {
	var info ffprobeAudioStreams
	if err := json.Unmarshal(output, &info); err != nil {
		return nil, fmt.Errorf("parseAudioTracks: %w", err)
	}
	if len(info.Streams) == 0 {
		return nil, ErrNoAudioTracks
	}
	tracks := make([]AudioTrack, 0, len(info.Streams))
	for i, s := range info.Streams {
		tracks = append(tracks, AudioTrack{
			Number:   i + 1,
			Language: strings.TrimSpace(s.Tags.Language),
			Title:    strings.TrimSpace(s.Tags.Title),
			Codec:    s.CodecName,
			Channels: s.Channels,
			Default:  s.Disposition.Default == 1,
		})
	}
	return tracks, nil
}

// DefaultAudioTrack returns the track a renderer plays without an override:
// the first stream flagged default, otherwise the first stream.
func DefaultAudioTrack(tracks []AudioTrack) int {
	for _, t := range tracks {
		if t.Default {
			return t.Number
		}
	}
	if len(tracks) > 0 {
		return tracks[0].Number
	}
	return 0
}

// AudioTrackOverride maps a chosen track to TranscodeOptions.AudioTrack.
// Choosing the source default needs no re-stream, so it returns 0.
func AudioTrackOverride(tracks []AudioTrack, chosen int) int {
	if chosen <= 0 || chosen > len(tracks) || chosen == DefaultAudioTrack(tracks) {
		return 0
	}
	return chosen
}

// Label is a compact, human description, e.g. "English · AC3 5.1".
func (t AudioTrack) Label() string {
	name := languageName(t.Language)
	var parts []string
	switch {
	case t.Title != "" && name != "" && !strings.Contains(strings.ToLower(t.Title), strings.ToLower(name)):
		parts = append(parts, t.Title+" ("+name+")")
	case t.Title != "":
		parts = append(parts, t.Title)
	case name != "":
		parts = append(parts, name)
	default:
		parts = append(parts, "Track "+strconv.Itoa(t.Number))
	}
	if format := strings.TrimSpace(audioCodecName(t.Codec) + " " + channelLayoutName(t.Channels)); format != "" {
		parts = append(parts, format)
	}
	return strings.Join(parts, " · ")
}

var audioLanguageNames = map[string]string{
	"ara": "Arabic", "bul": "Bulgarian", "ces": "Czech", "cze": "Czech", "chi": "Chinese", "zho": "Chinese",
	"dan": "Danish", "deu": "German", "ger": "German", "ell": "Greek", "gre": "Greek", "eng": "English",
	"spa": "Spanish", "est": "Estonian", "fin": "Finnish", "fra": "French", "fre": "French", "heb": "Hebrew",
	"hin": "Hindi", "hrv": "Croatian", "hun": "Hungarian", "ind": "Indonesian", "ita": "Italian", "jpn": "Japanese",
	"kor": "Korean", "lav": "Latvian", "lit": "Lithuanian", "msa": "Malay", "may": "Malay", "nld": "Dutch",
	"dut": "Dutch", "nor": "Norwegian", "nob": "Norwegian", "pol": "Polish", "por": "Portuguese", "ron": "Romanian",
	"rum": "Romanian", "rus": "Russian", "slk": "Slovak", "slo": "Slovak", "slv": "Slovenian", "srp": "Serbian",
	"swe": "Swedish", "tha": "Thai", "tur": "Turkish", "ukr": "Ukrainian", "vie": "Vietnamese", "cat": "Catalan",
	"fil": "Filipino", "tam": "Tamil", "tel": "Telugu", "urd": "Urdu", "per": "Persian", "fas": "Persian",
}

func languageName(code string) string {
	switch c := strings.ToLower(code); c {
	case "", "und", "unk", "mis", "zxx":
		return ""
	default:
		if name, ok := audioLanguageNames[c]; ok {
			return name
		}
		return strings.ToUpper(c)
	}
}

func audioCodecName(codec string) string {
	switch {
	case codec == "":
		return ""
	case codec == "eac3":
		return "E-AC3"
	case codec == "truehd":
		return "TrueHD"
	case codec == "opus":
		return "Opus"
	case codec == "vorbis":
		return "Vorbis"
	case strings.HasPrefix(codec, "pcm_"):
		return "PCM"
	case strings.HasPrefix(codec, "dts"):
		return "DTS"
	default:
		return strings.ToUpper(codec)
	}
}

func channelLayoutName(channels int) string {
	switch channels {
	case 0:
		return ""
	case 1:
		return "Mono"
	case 2:
		return "Stereo"
	case 6:
		return "5.1"
	case 8:
		return "7.1"
	default:
		return strconv.Itoa(channels) + "ch"
	}
}

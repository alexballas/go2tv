package playback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"go2tv.app/go2tv/v2/utils"
)

// Relative skip steps shared by every surface: back 10s, forward 30s.
const (
	SkipBackSeconds    = 10
	SkipForwardSeconds = 30
)

// SkipTarget applies a relative skip. With a known duration the target stays
// one second short of the end, so a forward skip never finishes the media.
func SkipTarget(current, delta, duration int) int {
	target := max(current+delta, 0)
	if duration > 0 {
		target = min(target, max(duration-1, 0))
	}
	return target
}

// ErrAudioTrackNotFound reports a requested track the media does not have.
var ErrAudioTrackNotFound = errors.New("audio track not found")

// AudioTrackList is a numbered, human-readable listing of tracks.
func AudioTrackList(tracks []utils.AudioTrack) string {
	defaultTrack := utils.DefaultAudioTrack(tracks)
	var b strings.Builder
	for _, track := range tracks {
		b.WriteString(strconv.Itoa(track.Number) + "  " + track.Label())
		if track.Number == defaultTrack {
			b.WriteString("  (default)")
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// ResolveAudioTrack validates a one-based track choice for media and returns
// the transcode override, zero when the choice is the source default, and
// the chosen track's label.
func ResolveAudioTrack(ctx context.Context, ffmpeg, media string, chosen int) (int, string, error) {
	if chosen <= 0 {
		return 0, "", nil
	}
	tracks, err := utils.AudioTracksContext(ctx, ffmpeg, media)
	if err != nil {
		return 0, "", fmt.Errorf("ResolveAudioTrack: %w", err)
	}
	if chosen > len(tracks) {
		return 0, "", fmt.Errorf("ResolveAudioTrack: %w: %d; available:\n%s", ErrAudioTrackNotFound, chosen, AudioTrackList(tracks))
	}
	return utils.AudioTrackOverride(tracks, chosen), tracks[chosen-1].Label(), nil
}

// AudioChoice is a CLI -a selection resolved against the media.
type AudioChoice struct {
	Track int // One-based override; zero plays the source default.
	Label string
}

// ResolveCLIAudioTrack validates -a for a local video. A non-default track
// is transcoded through FFmpeg.
func ResolveCLIAudioTrack(ctx context.Context, chosen int, ffmpeg string, ffmpegErr error, media, mediaType string, local bool) (AudioChoice, error) {
	if chosen <= 0 {
		return AudioChoice{}, nil
	}
	if !local || !strings.HasPrefix(mediaType, "video/") {
		return AudioChoice{}, errors.New("audio track selection (-a) needs a local video file (-v)")
	}
	if ffmpegErr != nil {
		return AudioChoice{}, fmt.Errorf("audio track selection needs ffmpeg: %w", ffmpegErr)
	}
	track, label, err := ResolveAudioTrack(ctx, ffmpeg, media, chosen)
	if err != nil || track == 0 {
		return AudioChoice{}, err
	}
	return AudioChoice{Track: track, Label: label}, nil
}

// PrintAudioTracks writes the -la listing for media.
func PrintAudioTracks(ctx context.Context, w io.Writer, ffmpeg, media string) error {
	if media == "" {
		return errors.New("-la needs a media file (-v)")
	}
	tracks, err := utils.AudioTracksContext(ctx, ffmpeg, media)
	if errors.Is(err, utils.ErrNoAudioTracks) {
		_, err = fmt.Fprintln(w, "No audio tracks.")
		return err
	}
	if err != nil {
		return fmt.Errorf("PrintAudioTracks: %w", err)
	}
	_, err = io.WriteString(w, AudioTrackList(tracks))
	return err
}

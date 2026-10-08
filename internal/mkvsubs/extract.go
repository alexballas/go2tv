package mkvsubs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
)

type fileSource struct {
	path string
	size int64
}

func (s fileSource) Open(context.Context) (io.ReadSeekCloser, error) { return os.Open(s.path) }
func (s fileSource) URL() string                                     { return s.path }
func (s fileSource) MIME() string                                    { return "video/x-matroska" }
func (s fileSource) Size() int64                                     { return s.size }

// ExtractFile writes a complete SRT from a local Matroska UTF8 track. Index is
// the subtitle ordinal, including unsupported tracks, matching FFmpeg's 0:s:n.
// StartTime is the media timeline origin reported by ffprobe. ASS/SSA need
// FFmpeg to preserve styling. Errors require callers to discard
// partial output and fall back to their existing extraction path.
func ExtractFile(ctx context.Context, path string, index int, startTime float64, out io.Writer) error {
	if index < 0 {
		return fmt.Errorf("invalid subtitle index: %d", index)
	}
	if math.IsNaN(startTime) || math.IsInf(startTime, 0) {
		return fmt.Errorf("invalid subtitle timeline origin")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("subtitle source must be a regular file")
	}
	r, err := os.Open(path)
	if err != nil {
		return err
	}
	defer r.Close()
	// Inspect the actual container, regardless of its filename extension.
	header, err := next(r, info.Size())
	if err != nil {
		return err
	}
	if header.id != 0x1a45dfa3 {
		return fmt.Errorf("not a Matroska file")
	}
	var docType string
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		e, err := next(r, header.end)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if e.id == 0x4282 {
			b, err := bytesValue(r, e, 32)
			if err != nil {
				return err
			}
			docType = string(b)
		}
		if err := skip(r, e); err != nil {
			return err
		}
	}
	if docType != "matroska" && docType != "webm" {
		return fmt.Errorf("unsupported EBML document type: %q", docType)
	}
	p := New(fileSource{path, info.Size()})
	p.subtitleIndex, p.strict = index, true
	if err := p.metadata(ctx, r); err != nil {
		return err
	}
	if p.track == 0 || p.codec != "S_TEXT/UTF8" {
		return ErrNoSubtitles
	}
	if _, err := r.Seek(p.first, io.SeekStart); err != nil {
		return err
	}
	var captions []Cue
	textBytes := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		e, err := next(r, p.end)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if e.id != clusterID {
			if err := skip(r, e); err != nil {
				return err
			}
			continue
		}
		_, cues, err := p.readCluster(ctx, r, e, math.Inf(-1), math.Inf(1))
		if err != nil {
			return err
		}
		for _, cue := range cues {
			textBytes += len(cue.Text)
		}
		captions = append(captions, cues...)
		// Complete tracks may exceed the progressive window's 4096-cue limit.
		if len(captions) > 1_000_000 || textBytes > 64<<20 {
			return fmt.Errorf("oversized subtitle track")
		}
	}
	sort.SliceStable(captions, func(i, j int) bool { return captions[i].Start < captions[j].Start })
	for i, cue := range captions {
		if err := ctx.Err(); err != nil {
			return err
		}
		start, end := math.Round((cue.Start-startTime)*1000), math.Round((cue.End-startTime)*1000)
		if math.IsInf(end, 0) || end >= math.MaxInt64 || start < 0 || end <= start {
			return fmt.Errorf("unsupported subtitle timestamp")
		}
		if _, err := fmt.Fprintf(out, "%d\n%s --> %s\n%s\n\n", i+1, srtTimestamp(int64(start)), srtTimestamp(int64(end)), cue.Text); err != nil {
			return err
		}
	}
	return nil
}

func srtTimestamp(milliseconds int64) string {
	return fmt.Sprintf("%02d:%02d:%02d,%03d", milliseconds/3600000, milliseconds/60000%60, milliseconds/1000%60, milliseconds%1000)
}

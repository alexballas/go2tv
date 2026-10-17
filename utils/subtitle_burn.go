package utils

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"go2tv.app/go2tv/v2/internal/mediasource"
)

const (
	maxSubtitleFonts     = 128
	maxSubtitleFontSize  = 16 << 20
	maxSubtitleFontBytes = 64 << 20
	localSubtitleBuffer  = 2 << 10
)

type localSubtitleSource struct {
	path string
	size int64
}

func (s localSubtitleSource) Open(ctx context.Context) (io.ReadSeekCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}
	section := io.NewSectionReader(file, 0, s.size)
	return &localSubtitleReader{file: file, section: section, reader: bufio.NewReaderSize(section, localSubtitleBuffer), size: s.size}, nil
}
func (s localSubtitleSource) URL() string  { return s.path }
func (s localSubtitleSource) MIME() string { return "video/x-matroska" }
func (s localSubtitleSource) Size() int64  { return s.size }

// EBML parsing repeatedly reads tiny headers and asks for the current offset.
// Keep offsets in memory and buffer adjacent headers and small audio blocks.
// Limit read-ahead so skipping large video blocks never reads their full payload.
type localSubtitleReader struct {
	file    *os.File
	section *io.SectionReader
	reader  *bufio.Reader
	pos     int64
	size    int64
}

func (r *localSubtitleReader) Read(data []byte) (int, error) {
	n, err := r.reader.Read(data)
	r.pos += int64(n)
	return n, err
}

func (r *localSubtitleReader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += r.pos
	case io.SeekEnd:
		offset += r.size
	default:
		return r.pos, os.ErrInvalid
	}
	if offset < 0 {
		return r.pos, os.ErrInvalid
	}
	if offset >= r.pos && offset-r.pos <= int64(r.reader.Buffered()) {
		n, err := r.reader.Discard(int(offset - r.pos))
		r.pos += int64(n)
		return r.pos, err
	}
	pos, err := r.section.Seek(offset, io.SeekStart)
	if err != nil {
		return r.pos, err
	}
	r.reader.Reset(r.section)
	r.pos = pos
	return pos, nil
}

func (r *localSubtitleReader) Close() error { return r.file.Close() }

func localTextBurnSource(selected *EmbeddedSubtitle) mediasource.Source {
	if _, progressive := mediasource.Lookup(selected.Path); progressive {
		return nil
	}
	switch strings.ToLower(filepath.Ext(selected.Path)) {
	case ".mkv", ".webm":
	default:
		return nil
	}
	info, err := os.Stat(selected.Path)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	return localSubtitleSource{path: selected.Path, size: info.Size()}
}

// EmbeddedSubtitleForBurn selects supported text and bitmap tracks for direct
// container rendering, retaining subtitle timestamps and ASS/SSA typesetting.
func EmbeddedSubtitleForBurn(ffmpegPath, path string, track int) (*EmbeddedSubtitle, error) {
	return EmbeddedSubtitleForBurnContext(context.Background(), ffmpegPath, path, track)
}

func EmbeddedSubtitleForBurnContext(ctx context.Context, ffmpegPath, path string, track int) (*EmbeddedSubtitle, error) {
	result, err := probeSubtitleTrackContext(ctx, ffmpegPath, path, track)
	if err != nil {
		return nil, err
	}
	if len(result.Streams) == 1 {
		codec := result.Streams[0].CodecName
		bitmap := bitmapSubtitleCodec(codec)
		plainText := plainTextSubtitleCodec(codec)
		if codec == "ass" || codec == "ssa" || bitmap || plainText {
			selected := &EmbeddedSubtitle{Path: path, Track: track, Bitmap: bitmap, PlainText: plainText}
			if !bitmap && result.Format.StartTime != "" {
				selected.StartTime, err = strconv.ParseFloat(result.Format.StartTime, 64)
				if err != nil || math.IsNaN(selected.StartTime) || math.IsInf(selected.StartTime, 0) {
					return nil, fmt.Errorf("invalid embedded subtitle timeline origin: %q", result.Format.StartTime)
				}
			}
			return selected, nil
		}
	}
	return nil, nil
}

func plainTextSubtitleCodec(codec string) bool {
	switch codec {
	case "subrip", "text", "mov_text", "webvtt", "microdvd", "jacosub", "sami",
		"realtext", "subviewer", "subviewer1", "vplayer", "mpl2", "pjs", "stl":
		return true
	}
	return false
}

func styledSubtitlePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ass", ".ssa":
		return true
	}
	return false
}

// libass reads every file in fontsdir. Stage only adjacent fonts so a sidecar
// beside videos never causes the media library to be loaded as font data.
func copySubtitleFonts(ctx context.Context, sourceDir string) (dir string, err error) {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil && dir != "" {
			_ = os.RemoveAll(dir)
		}
	}()
	count, total := 0, int64(0)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return dir, err
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		switch ext {
		case ".ttf", ".otf", ".ttc", ".otc", ".woff", ".woff2":
		default:
			continue
		}
		path := filepath.Join(sourceDir, entry.Name())
		info, err := os.Stat(path)
		if err != nil {
			return dir, err
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			continue
		}
		total += info.Size()
		if count >= maxSubtitleFonts || info.Size() > maxSubtitleFontSize || total > maxSubtitleFontBytes {
			return dir, fmt.Errorf("oversized subtitle fonts")
		}
		if dir == "" {
			dir, err = os.MkdirTemp("", "go2tv-subtitle-fonts-*")
			if err != nil {
				return dir, err
			}
		}
		input, err := os.Open(path)
		if err != nil {
			return dir, err
		}
		output, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("font-%03d%s", count, ext)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			_ = input.Close()
			return dir, err
		}
		_, copyErr := io.CopyN(output, input, info.Size())
		inputErr, outputErr := input.Close(), output.Close()
		if err := errors.Join(copyErr, inputErr, outputErr); err != nil {
			return dir, err
		}
		count++
	}
	return dir, nil
}

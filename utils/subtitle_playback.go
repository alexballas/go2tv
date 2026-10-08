package utils

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/saintfish/chardet"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/htmlindex"
	textunicode "golang.org/x/text/encoding/unicode"
)

var webVTTCueTiming = regexp.MustCompile(`(?m)^((?:\d{2,}:)?\d{2}:\d{2}\.\d{3})[\t ]+-->[\t ]+((?:\d{2,}:)?\d{2}:\d{2}\.\d{3})([^\n]*)$`)

// SubtitlesForPlayback returns WebVTT with cues relative to the start of a
// transcoded stream. Native playback uses a zero seek offset.
func SubtitlesForPlayback(path string, seekSeconds int, ffmpegPath ...string) ([]byte, error) {
	return SubtitlesForPlaybackContext(context.Background(), path, seekSeconds, ffmpegPath...)
}

// SubtitlesForPlaybackContext converts subtitles for receiver playback.
func SubtitlesForPlaybackContext(ctx context.Context, path string, seekSeconds int, ffmpegPath ...string) ([]byte, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".srt" && ext != ".vtt" && !styledSubtitlePath(path) {
		return nil, fmt.Errorf("unsupported subtitle format: %s", ext)
	}
	source, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	return SubtitlesReaderForPlaybackContext(ctx, source, filepath.Ext(path), seekSeconds, ffmpegPath...)
}

// SubtitlesReaderForPlayback supports files and mobile document providers.
// Each call reads the original captions, so repeated seeks never compound offsets.
func SubtitlesReaderForPlayback(source io.Reader, extension string, seekSeconds int, ffmpegPath ...string) ([]byte, error) {
	return SubtitlesReaderForPlaybackContext(context.Background(), source, extension, seekSeconds, ffmpegPath...)
}

// SubtitlesReaderForPlaybackContext converts captions and honors cancellation.
func SubtitlesReaderForPlaybackContext(ctx context.Context, source io.Reader, extension string, seekSeconds int, ffmpegPath ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stopClosing := closeSubtitleSourceOnCancel(ctx, source)
	defer stopClosing()
	var data []byte
	var err error
	switch strings.ToLower(extension) {
	case ".srt":
		data, err = ConvertSRTReaderToWebVTT(source)
	case ".vtt":
		data, err = io.ReadAll(source)
	case ".ass", ".ssa":
		path := ""
		if len(ffmpegPath) > 0 {
			path = ffmpegPath[0]
		}
		data, err = convertASSReaderToWebVTT(ctx, source, path)
	default:
		return nil, fmt.Errorf("unsupported subtitle format: %s", extension)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	if seekSeconds <= 0 {
		return data, nil
	}

	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	blocks := strings.Split(text, "\n\n")
	kept := make([]string, 0, len(blocks))
	offset := int64(seekSeconds) * 1000
	for _, block := range blocks {
		// Comments and style blocks may contain text resembling cue timings.
		if strings.HasPrefix(block, "NOTE") || strings.HasPrefix(block, "STYLE") || strings.HasPrefix(block, "REGION") {
			kept = append(kept, block)
			continue
		}
		match := webVTTCueTiming.FindStringSubmatchIndex(block)
		if match != nil {
			start := webVTTMilliseconds(block[match[2]:match[3]]) - offset
			end := webVTTMilliseconds(block[match[4]:match[5]]) - offset
			if end <= 0 {
				continue
			}
			timing := webVTTTimestamp(max(start, 0)) + " --> " + webVTTTimestamp(end) + block[match[6]:match[7]]
			block = block[:match[0]] + timing + block[match[1]:]
		}
		kept = append(kept, block)
	}
	return []byte(strings.Join(kept, "\n\n")), nil
}

// ConvertASSReaderToSRT converts ASS/SSA captions for DLNA renderers.
func ConvertASSReaderToSRT(ctx context.Context, source io.Reader, ffmpegPath string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stopClosing := closeSubtitleSourceOnCancel(ctx, source)
	defer stopClosing()
	prepared, err := prepareASSForConversion(ctx, source)
	if err != nil {
		return nil, err
	}
	return convertSubtitleWithFFmpeg(ctx, bytes.NewReader(prepared), "ass", "srt", ffmpegPath)
}

func closeSubtitleSourceOnCancel(ctx context.Context, source io.Reader) func() bool {
	closer, ok := source.(io.Closer)
	if !ok {
		return func() bool { return true }
	}
	return context.AfterFunc(ctx, func() { _ = closer.Close() })
}

func convertASSReaderToWebVTT(ctx context.Context, source io.Reader, ffmpegPath string) ([]byte, error) {
	prepared, err := prepareASSForConversion(ctx, source)
	if err != nil {
		return nil, err
	}
	return convertSubtitleWithFFmpeg(ctx, bytes.NewReader(prepared), "ass", "webvtt", ffmpegPath)
}

func convertSubtitleWithFFmpeg(ctx context.Context, source io.Reader, inputFormat, outputFormat, ffmpegPath string) ([]byte, error) {
	path, err := ResolveFFmpegPath(ffmpegPath)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, "-nostdin", "-v", "error", "-f", inputFormat, "-i", "pipe:0", "-map", "0:s:0", "-f", outputFormat, "pipe:1")
	command.WaitDelay = time.Second
	setSysProcAttr(command)
	command.Stdin = source
	data, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("convert %s captions: %w", inputFormat, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

func prepareASSForConversion(ctx context.Context, source io.Reader) ([]byte, error) {
	data, err := io.ReadAll(source)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("read ASS captions: %w", err)
	}
	data, err = decodeASSCharset(data)
	if err != nil {
		return nil, err
	}
	return stripASSDrawings(data), nil
}

func decodeASSCharset(data []byte) ([]byte, error) {
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		return data[3:], nil
	}
	if bytes.HasPrefix(data, []byte{0xff, 0xfe}) || bytes.HasPrefix(data, []byte{0xfe, 0xff}) {
		decoded, err := textunicode.UTF16(textunicode.LittleEndian, textunicode.ExpectBOM).NewDecoder().Bytes(data)
		if err != nil {
			return nil, fmt.Errorf("decode UTF-16 ASS captions: %w", err)
		}
		return decoded, nil
	}
	if endian := utf16WithoutBOM(data); endian != nil {
		decoded, err := textunicode.UTF16(*endian, textunicode.IgnoreBOM).NewDecoder().Bytes(data)
		if err != nil {
			return nil, fmt.Errorf("decode UTF-16 ASS captions: %w", err)
		}
		return decoded, nil
	}
	if utf8.Valid(data) {
		return data, nil
	}
	guess, err := chardet.NewTextDetector().DetectBest(assCharsetSample(data))
	if err != nil {
		// A single accented cue is too short for statistical detection.
		// Windows-1252 preserves common Western legacy subtitles in that case.
		decoded, decodeErr := charmap.Windows1252.NewDecoder().Bytes(data)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode fallback ASS charset: %w", decodeErr)
		}
		return decoded, nil
	}
	encoding, err := htmlindex.Get(guess.Charset)
	if err != nil {
		return nil, fmt.Errorf("unsupported ASS charset %q: %w", guess.Charset, err)
	}
	decoded, err := encoding.NewDecoder().Bytes(data)
	if err != nil {
		return nil, fmt.Errorf("decode ASS charset %q: %w", guess.Charset, err)
	}
	return decoded, nil
}

// ASS headers are mostly ASCII and can overwhelm charset detection. Sample
// dialogue text, where the language signal normally lives.
func assCharsetSample(data []byte) []byte {
	var sample bytes.Buffer
	for line := range bytes.SplitSeq(data, []byte{'\n'}) {
		prefix, fields, ok := bytes.Cut(bytes.TrimSpace(line), []byte{':'})
		if !ok || !bytes.EqualFold(prefix, []byte("Dialogue")) {
			continue
		}
		for range 9 {
			_, fields, ok = bytes.Cut(fields, []byte{','})
			if !ok {
				break
			}
		}
		if ok {
			sample.Write(fields)
			sample.WriteByte('\n')
		}
	}
	if sample.Len() > 0 {
		return sample.Bytes()
	}
	return data
}

func utf16WithoutBOM(data []byte) *textunicode.Endianness {
	sample := data[:min(len(data), 128)]
	if len(sample) < 16 {
		return nil
	}
	var even, odd int
	for i, b := range sample {
		if b == 0 {
			if i%2 == 0 {
				even++
			} else {
				odd++
			}
		}
	}
	if odd > len(sample)/4 && even < len(sample)/16 {
		endian := textunicode.LittleEndian
		return &endian
	}
	if even > len(sample)/4 && odd < len(sample)/16 {
		endian := textunicode.BigEndian
		return &endian
	}
	return nil
}

func stripASSDrawings(data []byte) []byte {
	var result strings.Builder
	result.Grow(len(data))
	inEvents := false
	textField := 9 // ASS and SSA default event formats both put Text tenth.
	for line := range strings.SplitAfterSeq(string(data), "\n") {
		body := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		end := line[len(body):]
		trimmed := strings.TrimSpace(body)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inEvents = strings.EqualFold(trimmed, "[Events]")
		}
		if !inEvents {
			result.WriteString(line)
			continue
		}
		prefix, value, hasColon := strings.Cut(body, ":")
		switch {
		case hasColon && strings.EqualFold(strings.TrimSpace(prefix), "Format"):
			fields := strings.Split(value, ",")
			for i, field := range fields {
				if strings.EqualFold(strings.TrimSpace(field), "Text") {
					textField = i
					break
				}
			}
		case hasColon && strings.EqualFold(strings.TrimSpace(prefix), "Dialogue"):
			fields := strings.SplitN(value, ",", textField+1)
			if len(fields) > textField {
				cleaned, hadDrawing := removeASSDrawingRuns(fields[textField])
				if hadDrawing {
					if !hasVisibleASSText(cleaned) {
						continue
					}
					fields[textField] = cleaned
					body = prefix + ":" + strings.Join(fields, ",")
				}
			}
		}
		result.WriteString(body)
		result.WriteString(end)
	}
	return []byte(result.String())
}

func removeASSDrawingRuns(text string) (string, bool) {
	var result strings.Builder
	result.Grow(len(text))
	drawing, hadDrawing := false, false
	for len(text) > 0 {
		if text[0] == '{' {
			if end := strings.IndexByte(text, '}'); end >= 0 {
				block := text[:end+1]
				for i := 0; i+2 < len(block); i++ {
					if block[i] != '\\' || (block[i+1] != 'p' && block[i+1] != 'P') {
						continue
					}
					j := i + 2
					for j < len(block) && (block[j] == ' ' || block[j] == '\t') {
						j++
					}
					start := j
					for j < len(block) && block[j] >= '0' && block[j] <= '9' {
						j++
					}
					if j > start {
						drawing = block[start:j] != "0"
						hadDrawing = true
					}
				}
				result.WriteString(block)
				text = text[end+1:]
				continue
			}
		}
		end := strings.IndexByte(text, '{')
		if end < 0 {
			end = len(text)
		}
		if end == 0 { // Unclosed brace is literal text.
			end = 1
		}
		if !drawing {
			result.WriteString(text[:end])
		}
		text = text[end:]
	}
	return result.String(), hadDrawing
}

func hasVisibleASSText(text string) bool {
	for len(text) > 0 {
		if text[0] == '{' {
			if end := strings.IndexByte(text, '}'); end >= 0 {
				text = text[end+1:]
				continue
			}
		}
		if strings.HasPrefix(text, `\N`) || strings.HasPrefix(text, `\n`) || strings.HasPrefix(text, `\h`) {
			text = text[2:]
			continue
		}
		r, size := utf8.DecodeRuneInString(text)
		if !unicode.IsSpace(r) {
			return true
		}
		text = text[size:]
	}
	return false
}

func webVTTMilliseconds(timestamp string) int64 {
	parts := strings.FieldsFunc(timestamp, func(r rune) bool { return r == ':' || r == '.' })
	var seconds int64
	for _, part := range parts[:len(parts)-1] {
		value, _ := strconv.ParseInt(part, 10, 64)
		seconds = seconds*60 + value
	}
	milliseconds, _ := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	return seconds*1000 + milliseconds
}

func webVTTTimestamp(milliseconds int64) string {
	return fmt.Sprintf("%02d:%02d:%02d.%03d", milliseconds/3600000, milliseconds/60000%60, milliseconds/1000%60, milliseconds%1000)
}

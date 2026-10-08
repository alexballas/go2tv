package mkvsubs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"go2tv.app/go2tv/v2/internal/mediasource"
)

const (
	segmentID     = 0x18538067
	clusterID     = 0x1f43b675
	cuesID        = 0x1c53bb6b
	attachmentsID = 0x1941a469
	maxText       = 64 << 10
	maxCues       = 4096
)

var ErrNoSubtitles = errors.New("no supported Matroska text subtitles")

// IncompleteWindow preserves captions already read when a future piece is
// unavailable. Until tells the receiver to refresh this partial window soon.
type IncompleteWindow struct {
	Until float64
	err   error
}

func (e *IncompleteWindow) Error() string { return e.err.Error() }
func (e *IncompleteWindow) Unwrap() error { return e.err }

type Cue struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type cluster struct {
	pos  int64
	time float64
}

// Parser caches metadata, cluster offsets and overlapping captions. Each window
// opens a cancellable reader and skips audio/video bodies. Torrent pieces may
// contain video bytes alongside the requested metadata.
type Parser struct {
	source                    mediasource.Source
	gate                      chan struct{}
	ready                     bool
	track                     uint64
	codec                     string
	scale                     float64
	defaultDuration           float64
	segment, end, first, cues int64
	index                     []cluster
	indexed                   bool
	active                    []Cue
	subtitleIndex             int  // -1 selects the first supported track.
	strict                    bool // File export must fail rather than silently omit captions.
	preserveASS               bool
	burn                      BurnMetadata
	attachments               int64
	seekHeads                 []int64
}

func New(source mediasource.Source) *Parser {
	return &Parser{source: source, gate: make(chan struct{}, 1), scale: 1e-3, subtitleIndex: -1}
}

// PrepareBurn selects a text track and reads only the first cluster's timeline
// origin. Window supplies the captions later, as the encoder needs them.
func (p *Parser) PrepareBurn(ctx context.Context) (float64, error) {
	r, err := p.source.Open(ctx)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	if reader, ok := r.(interface{ SetReadahead(int64) }); ok {
		reader.SetReadahead(4 << 10)
	}
	p.strict = true
	p.preserveASS = true
	if err := p.metadata(ctx, r); err != nil {
		return 0, err
	}
	if p.track == 0 {
		return 0, ErrNoSubtitles
	}
	p.ready = true
	// metadata leaves the reader immediately after the cluster header.
	if _, err := r.Seek(p.first, io.SeekStart); err != nil {
		return 0, err
	}
	parent, err := next(r, p.end)
	if err != nil {
		return 0, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		e, err := next(r, parent.end)
		if err != nil {
			return 0, err
		}
		if e.id == 0xe7 {
			ticks, err := uintValue(r, e)
			return float64(ticks) * p.scale, err
		}
		if err := skip(r, e); err != nil {
			return 0, err
		}
	}
}

func (p *Parser) Window(ctx context.Context, start, end float64) ([]Cue, error) {
	select {
	case p.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-p.gate }()
	r, err := p.source.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	// Sparse metadata reads must not prefetch megabytes of video per seek.
	if reader, ok := r.(interface{ SetReadahead(int64) }); ok {
		reader.SetReadahead(4 << 10)
	}
	if !p.ready {
		if err := p.metadata(ctx, r); err != nil {
			return nil, err
		}
		p.ready = true
	}
	if p.track == 0 {
		return nil, ErrNoSubtitles
	}
	frontier := float64(0)
	if len(p.index) > 0 {
		frontier = p.index[len(p.index)-1].time
	}
	if start > frontier+60 && !p.indexed && p.cues > 0 {
		if err := p.loadIndex(ctx, r); err != nil {
			return nil, err
		}
	}
	pos := p.first
	// Include earlier clusters for captions overlapping a seek. Sequential
	// playback retains the index and never traverses the complete media payload.
	for _, c := range p.index {
		if c.time > start-120 {
			break
		}
		pos = c.pos
	}
	if _, err := r.Seek(pos, io.SeekStart); err != nil {
		return nil, err
	}
	captions := make([]Cue, 0)
	until := start
	var incomplete error
	for {
		if err := ctx.Err(); err != nil {
			incomplete = err
			break
		}
		e, err := next(r, p.end)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				incomplete = ctx.Err()
				break
			}
			return nil, err
		}
		if e.id == clusterID {
			time, cues, err := p.readCluster(ctx, r, e, start, end)
			captions = append(captions, cues...)
			until = max(until, time)
			if err != nil {
				if ctx.Err() != nil {
					incomplete = ctx.Err()
					break
				}
				return nil, err
			}
			p.remember(cluster{e.pos, time})
			if len(captions) > maxCues {
				return nil, fmt.Errorf("too many subtitle cues")
			}
			if time >= end+32768*p.scale {
				break
			}
		} else if err := skip(r, e); err != nil {
			return nil, err
		}
	}
	seen := make(map[Cue]bool, len(captions))
	for _, cue := range captions {
		seen[cue] = true
	}
	for _, cue := range p.active {
		if cue.Start < end && cue.End > start && !seen[cue] {
			captions = append(captions, cue)
			seen[cue] = true
		}
	}
	textBytes := 0
	for _, cue := range captions {
		textBytes += len(cue.Text)
	}
	if len(captions) > maxCues || textBytes > 1<<20 {
		return nil, fmt.Errorf("oversized subtitle window")
	}
	sort.SliceStable(captions, func(i, j int) bool { return captions[i].Start < captions[j].Start })
	// HTTP callers adjust returned cues into the stream's timeline. Preserve
	// source timestamps in the cache independently of those adjustments.
	p.active = append([]Cue(nil), captions...)
	if incomplete != nil {
		if len(captions) == 0 {
			return nil, incomplete
		}
		return captions, &IncompleteWindow{Until: min(end, max(start+1, until)), err: incomplete}
	}
	return captions, nil
}

func (p *Parser) remember(c cluster) {
	i := sort.Search(len(p.index), func(i int) bool { return p.index[i].pos >= c.pos })
	if i < len(p.index) && p.index[i].pos == c.pos {
		return
	}
	if len(p.index) >= 100000 {
		return
	}
	p.index = append(p.index, cluster{})
	copy(p.index[i+1:], p.index[i:])
	p.index[i] = c
}

func (p *Parser) metadata(ctx context.Context, r io.ReadSeeker) error {
	p.track, p.codec, p.first, p.cues = 0, "", 0, 0
	p.burn = BurnMetadata{}
	p.attachments, p.seekHeads = 0, nil
	p.scale = 1e-3
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		e, err := next(r, p.source.Size())
		if err != nil {
			return err
		}
		if e.id != segmentID {
			if err := skip(r, e); err != nil {
				return err
			}
			continue
		}
		p.segment, p.end = e.data, e.end
		break
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		e, err := next(r, p.end)
		if errors.Is(err, io.EOF) {
			return ErrNoSubtitles
		}
		if err != nil {
			return err
		}
		switch e.id {
		case 0x1549a966:
			err = p.info(r, e)
		case 0x1654ae6b:
			err = p.tracks(r, e)
		case 0x114d9b74:
			err = p.seekHead(r, e)
		case attachmentsID:
			p.attachments = e.pos
		case clusterID:
			p.first = e.pos
			return nil
		}
		if err != nil {
			return err
		}
		if err := skip(r, e); err != nil {
			return err
		}
	}
}

func (p *Parser) info(r io.ReadSeeker, parent element) error {
	for {
		e, err := next(r, parent.end)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if e.id == 0x2ad7b1 {
			v, err := uintValue(r, e)
			if err != nil {
				return err
			}
			if v == 0 {
				return fmt.Errorf("invalid timestamp scale")
			}
			p.scale = float64(v) / 1e9
		}
		if err := skip(r, e); err != nil {
			return err
		}
	}
}

func (p *Parser) tracks(r io.ReadSeeker, parent element) error {
	subtitleIndex := 0
	for {
		e, err := next(r, parent.end)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if e.id == 0xae {
			var number, kind, duration uint64
			var codec string
			var private element
			encoded := false
			for {
				c, err := next(r, e.end)
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					return err
				}
				switch c.id {
				case 0xd7:
					number, err = uintValue(r, c)
				case 0x83:
					kind, err = uintValue(r, c)
				case 0x23e383:
					duration, err = uintValue(r, c)
				case 0x86:
					var b []byte
					b, err = bytesValue(r, c, 128)
					codec = string(b)
				case 0x6d80:
					encoded = true
				case 0x63a2:
					if p.preserveASS {
						private = c
					}
				case 0xe0:
					if p.preserveASS && p.burn.Width == 0 {
						err = p.videoSize(r, c)
					}
				case 0x56aa, 0x23314f, 0x537f:
					// Codec delay, track timestamp scale/offset need FFmpeg's
					// timing adjustments when exporting a complete subtitle file.
					if p.strict {
						encoded = true
					}
				}
				if err != nil {
					return err
				}
				if err := skip(r, c); err != nil {
					return err
				}
			}
			if kind == 17 {
				selected := p.subtitleIndex < 0 || p.subtitleIndex == subtitleIndex
				if p.track == 0 && selected && !encoded && (codec == "S_TEXT/ASS" || codec == "S_TEXT/SSA" || codec == "S_TEXT/UTF8") {
					var header string
					if p.preserveASS && codec != "S_TEXT/UTF8" && private.size > 0 {
						if _, err := r.Seek(private.data, io.SeekStart); err != nil {
							return err
						}
						b, err := bytesValue(r, private, 1<<20)
						if err != nil {
							return err
						}
						if !utf8.Valid(b) {
							return fmt.Errorf("invalid UTF8 subtitle header")
						}
						header = string(b)
					}
					p.track, p.codec, p.defaultDuration = number, codec, float64(duration)/1e9
					p.burn.Codec, p.burn.Header = codec, header
				}
				subtitleIndex++
			}
		}
		if err := skip(r, e); err != nil {
			return err
		}
	}
}

func (p *Parser) seekHead(r io.ReadSeeker, parent element) error {
	for {
		e, err := next(r, parent.end)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if e.id == 0x4dbb {
			var id, pos uint64
			for {
				c, err := next(r, e.end)
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					return err
				}
				if c.id == 0x53ab {
					id, err = uintValue(r, c)
				}
				if c.id == 0x53ac {
					pos, err = uintValue(r, c)
				}
				if err != nil {
					return err
				}
				if err := skip(r, c); err != nil {
					return err
				}
			}
			if id == cuesID && pos < uint64(p.end-p.segment) {
				p.cues = p.segment + int64(pos)
			}
			if p.preserveASS && pos < uint64(p.end-p.segment) {
				switch id {
				case attachmentsID:
					p.attachments = p.segment + int64(pos)
				case 0x114d9b74:
					if len(p.seekHeads) < 16 {
						p.seekHeads = append(p.seekHeads, p.segment+int64(pos))
					}
				}
			}
		}
		if err := skip(r, e); err != nil {
			return err
		}
	}
}

func (p *Parser) loadIndex(ctx context.Context, r io.ReadSeeker) error {
	if _, err := r.Seek(p.cues, io.SeekStart); err != nil {
		return err
	}
	parent, err := next(r, p.end)
	if err != nil {
		return err
	}
	if parent.id != cuesID {
		return fmt.Errorf("invalid Matroska seek index")
	}
	index := make([]cluster, 0)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		e, err := next(r, parent.end)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if e.id == 0xbb {
			var ticks uint64
			var positions []uint64
			for {
				c, err := next(r, e.end)
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					return err
				}
				if c.id == 0xb3 {
					ticks, err = uintValue(r, c)
				}
				if c.id == 0xb7 {
					for {
						v, err := next(r, c.end)
						if errors.Is(err, io.EOF) {
							break
						}
						if err != nil {
							return err
						}
						if v.id == 0xf1 {
							pos, err := uintValue(r, v)
							if err != nil {
								return err
							}
							if pos < uint64(p.end-p.segment) {
								positions = append(positions, pos)
							}
						}
						if err := skip(r, v); err != nil {
							return err
						}
					}
				}
				if err != nil {
					return err
				}
				if err := skip(r, c); err != nil {
					return err
				}
			}
			for _, pos := range positions {
				index = append(index, cluster{p.segment + int64(pos), float64(ticks) * p.scale})
			}
		}
		if len(index) > 100000 {
			return fmt.Errorf("oversized subtitle seek index")
		}
		if err := skip(r, e); err != nil {
			return err
		}
	}
	p.index = append(p.index, index...)
	sort.SliceStable(p.index, func(i, j int) bool { return p.index[i].pos < p.index[j].pos })
	unique := p.index[:0]
	for _, c := range p.index {
		if len(unique) == 0 || unique[len(unique)-1].pos != c.pos {
			unique = append(unique, c)
		}
	}
	if len(unique) > 100000 {
		p.index = unique[:100000]
		return fmt.Errorf("oversized subtitle seek index")
	}
	p.index = unique
	p.indexed = true
	return nil
}

func (p *Parser) readCluster(ctx context.Context, r io.ReadSeeker, parent element, start, end float64) (float64, []Cue, error) {
	var ticks uint64
	var captions []Cue
	for {
		if err := ctx.Err(); err != nil {
			return float64(ticks) * p.scale, captions, err
		}
		e, err := next(r, parent.end)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return float64(ticks) * p.scale, captions, err
		}
		// Unknown-sized clusters end at the next Segment child.
		if e.id == clusterID || e.id == cuesID || e.id == 0x1549a966 || e.id == 0x1654ae6b || e.id == attachmentsID || e.id == 0x114d9b74 || e.id == 0x1254c367 || e.id == 0x1043a770 {
			_, err = r.Seek(e.pos, io.SeekStart)
			if err != nil {
				return float64(ticks) * p.scale, captions, err
			}
			break
		}
		if e.id == 0xe7 {
			ticks, err = uintValue(r, e)
			if err != nil {
				return float64(ticks) * p.scale, captions, err
			}
			// A block can start before its cluster's timestamp (signed int16).
			if float64(ticks)*p.scale >= end+32768*p.scale {
				return float64(ticks) * p.scale, nil, nil
			}
		}
		var text string
		var relative int16
		duration := p.defaultDuration
		switch e.id {
		case 0xa3:
			text, relative, err = p.block(r, e)
		case 0xa0:
			for {
				c, e2 := next(r, e.end)
				if errors.Is(e2, io.EOF) {
					break
				}
				if e2 != nil {
					return float64(ticks) * p.scale, captions, e2
				}
				switch c.id {
				case 0xa1:
					text, relative, err = p.block(r, c)
				case 0x9b:
					var d uint64
					d, err = uintValue(r, c)
					duration = float64(d) * p.scale
				}
				if err != nil {
					return float64(ticks) * p.scale, captions, err
				}
				if err := skip(r, c); err != nil {
					return float64(ticks) * p.scale, captions, err
				}
			}
		}
		if err != nil {
			return float64(ticks) * p.scale, captions, err
		}
		at := (float64(ticks) + float64(relative)) * p.scale
		if p.strict && text != "" && (duration <= 0 || at < 0) {
			return float64(ticks) * p.scale, captions, fmt.Errorf("unsupported subtitle timing")
		}
		if text != "" && duration > 0 && at < end && at+duration > start {
			captions = append(captions, Cue{at, at + duration, text})
			if len(captions) > maxCues {
				return float64(ticks) * p.scale, captions, fmt.Errorf("too many subtitle cues")
			}
		}
		if err := skip(r, e); err != nil {
			return float64(ticks) * p.scale, captions, err
		}
	}
	return float64(ticks) * p.scale, captions, nil
}

func (p *Parser) block(r io.ReadSeeker, e element) (string, int16, error) {
	track, _, err := vint(r, false)
	if err != nil {
		return "", 0, err
	}
	if track != p.track {
		return "", 0, nil
	}
	var header [3]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return "", 0, err
	}
	if header[2]&0x06 != 0 {
		return "", 0, fmt.Errorf("laced subtitles unsupported")
	}
	pos, err := r.Seek(0, io.SeekCurrent)
	if err != nil {
		return "", 0, err
	}
	if e.end < pos || e.end-pos > maxText {
		return "", 0, fmt.Errorf("oversized subtitle block")
	}
	b := make([]byte, e.end-pos)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", 0, err
	}
	if p.strict && !utf8.Valid(b) {
		return "", 0, fmt.Errorf("invalid UTF8 subtitle")
	}
	text := strings.ToValidUTF8(string(b), "�")
	if p.codec != "S_TEXT/UTF8" {
		fields := strings.SplitN(text, ",", 9)
		if len(fields) != 9 {
			return "", 0, fmt.Errorf("invalid ASS subtitle block")
		}
		if !p.preserveASS || p.burn.Header == "" {
			text = assText(fields[8])
		}
	}
	return text, int16(uint16(header[0])<<8 | uint16(header[1])), nil
}

func assText(s string) string {
	var out strings.Builder
	drawing := false
	for len(s) > 0 {
		if s[0] == '{' {
			if end := strings.IndexByte(s, '}'); end >= 0 {
				for _, tag := range strings.Split(s[1:end], "\\") {
					if strings.HasPrefix(tag, "p") && len(tag) > 1 && tag[1] >= '0' && tag[1] <= '9' {
						drawing = tag[1] != '0'
					}
				}
				s = s[end+1:]
				continue
			}
		}
		if !drawing {
			out.WriteByte(s[0])
		}
		s = s[1:]
	}
	return strings.TrimSpace(strings.NewReplacer("\\N", "\n", "\\n", "\n", "\\h", " ").Replace(out.String()))
}

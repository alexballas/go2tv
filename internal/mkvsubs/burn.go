package mkvsubs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxFonts     = 128
	maxFontSize  = 16 << 20
	maxFontBytes = 64 << 20
)

// BurnMetadata preserves the script and video geometry used by libass. For a
// styled burn, Window returns Matroska ASS packets in Cue.Text, including their
// read order, layer, style, margins, effects and unmodified override tags.
type BurnMetadata struct {
	Codec, Header string
	Width, Height int
}

func (m BurnMetadata) Styled() bool {
	return m.Header != "" && (m.Codec == "S_TEXT/ASS" || m.Codec == "S_TEXT/SSA")
}

func (p *Parser) BurnMetadata() BurnMetadata { return p.burn }

func (p *Parser) videoSize(r io.ReadSeeker, parent element) error {
	for {
		e, err := next(r, parent.end)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if e.id == 0xb0 || e.id == 0xba {
			v, err := uintValue(r, e)
			if err != nil {
				return err
			}
			if v == 0 || v > 32768 {
				return fmt.Errorf("invalid subtitle canvas size")
			}
			if e.id == 0xb0 {
				p.burn.Width = int(v)
			} else {
				p.burn.Height = int(v)
			}
		}
		if err := skip(r, e); err != nil {
			return err
		}
	}
}

// ExtractFonts reads only indexed attachment metadata and font bodies. It does
// not scan clusters to locate attachments placed after the playable media.
// Generated names keep container filenames from becoming filesystem paths.
func (p *Parser) ExtractFonts(ctx context.Context, dir string) error {
	if !p.burn.Styled() {
		return nil
	}
	r, err := p.source.Open(ctx)
	if err != nil {
		return err
	}
	defer r.Close()
	if reader, ok := r.(interface{ SetReadahead(int64) }); ok {
		reader.SetReadahead(4 << 10)
	}
	visited := make(map[int64]bool)
	for i := 0; p.attachments == 0 && i < len(p.seekHeads); i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		pos := p.seekHeads[i]
		if visited[pos] {
			continue
		}
		visited[pos] = true
		if _, err := r.Seek(pos, io.SeekStart); err != nil {
			return err
		}
		e, err := next(r, p.end)
		if err != nil {
			return err
		}
		if e.id != 0x114d9b74 {
			return fmt.Errorf("invalid Matroska attachment seek index")
		}
		if err := p.seekHead(r, e); err != nil {
			return err
		}
	}
	if p.attachments == 0 {
		return nil
	}
	if _, err := r.Seek(p.attachments, io.SeekStart); err != nil {
		return err
	}
	parent, err := next(r, p.end)
	if err != nil {
		return err
	}
	if parent.id != attachmentsID {
		return fmt.Errorf("invalid Matroska attachments")
	}
	count, total := 0, int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		e, err := next(r, parent.end)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if e.id == 0x61a7 {
			ext, data, err := fontAttachment(r, e)
			if err != nil {
				return err
			}
			if ext != "" && data.end > data.data {
				size := data.end - data.data
				total += size
				if count >= maxFonts || size > maxFontSize || total > maxFontBytes {
					return fmt.Errorf("oversized subtitle font attachments")
				}
				if _, err := r.Seek(data.data, io.SeekStart); err != nil {
					return err
				}
				file, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("font-%03d%s", count, ext)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					return err
				}
				_, copyErr := io.CopyN(file, r, size)
				closeErr := file.Close()
				if copyErr != nil {
					return copyErr
				}
				if closeErr != nil {
					return closeErr
				}
				count++
			}
		}
		if err := skip(r, e); err != nil {
			return err
		}
	}
}

func fontAttachment(r io.ReadSeeker, parent element) (string, element, error) {
	var name, mime string
	var data element
	for {
		e, err := next(r, parent.end)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", data, err
		}
		switch e.id {
		case 0x466e, 0x4660:
			b, err := bytesValue(r, e, 4096)
			if err != nil {
				return "", data, err
			}
			if e.id == 0x466e {
				name = string(b)
			} else {
				mime = strings.ToLower(string(b))
			}
		case 0x465c:
			data = e
		}
		if err := skip(r, e); err != nil {
			return "", data, err
		}
	}
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".ttf", ".otf", ".ttc", ".otc", ".woff", ".woff2":
		return ext, data, nil
	}
	switch mime {
	case "font/ttf", "font/otf", "font/collection", "font/sfnt", "font/woff", "font/woff2", "application/x-truetype-font", "application/vnd.ms-opentype", "application/x-font-ttf", "application/x-font-opentype", "application/font-sfnt", "application/font-woff":
		return ".ttf", data, nil
	}
	return "", data, nil
}

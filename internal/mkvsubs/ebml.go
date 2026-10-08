// Package mkvsubs reads text subtitles from verified, seekable Matroska sources.
package mkvsubs

import (
	"encoding/binary"
	"fmt"
	"io"
)

type element struct {
	id, size       uint64
	pos, data, end int64
}

func vint(r io.Reader, id bool) (uint64, int, error) {
	var b [8]byte
	if _, err := io.ReadFull(r, b[:1]); err != nil {
		return 0, 0, err
	}
	n, mask := 1, byte(0x80)
	for mask != 0 && b[0]&mask == 0 {
		n++
		mask >>= 1
	}
	if mask == 0 || (id && n > 4) {
		return 0, 0, fmt.Errorf("invalid EBML integer")
	}
	if _, err := io.ReadFull(r, b[1:n]); err != nil {
		return 0, 0, err
	}
	v := uint64(b[0])
	if !id {
		v &= uint64(mask - 1)
	}
	for _, c := range b[1:n] {
		v = v<<8 | uint64(c)
	}
	return v, n, nil
}

func next(r io.ReadSeeker, limit int64) (element, error) {
	pos, err := r.Seek(0, io.SeekCurrent)
	if err != nil {
		return element{}, err
	}
	if pos >= limit {
		return element{}, io.EOF
	}
	id, _, err := vint(r, true)
	if err != nil {
		return element{}, err
	}
	size, n, err := vint(r, false)
	if err != nil {
		return element{}, err
	}
	data, err := r.Seek(0, io.SeekCurrent)
	if err != nil {
		return element{}, err
	}
	end := limit
	if size != uint64(1)<<(7*n)-1 {
		if size > uint64(limit-data) || data > limit {
			return element{}, fmt.Errorf("EBML element exceeds container")
		}
		end = data + int64(size)
	} else if id != segmentID && id != clusterID {
		return element{}, fmt.Errorf("unsupported unknown-sized EBML element")
	}
	return element{id: id, size: size, pos: pos, data: data, end: end}, nil
}

func skip(r io.ReadSeeker, e element) error { _, err := r.Seek(e.end, io.SeekStart); return err }

func uintValue(r io.Reader, e element) (uint64, error) {
	if e.size > 8 {
		return 0, fmt.Errorf("oversized EBML integer")
	}
	var b [8]byte
	_, err := io.ReadFull(r, b[8-int(e.size):])
	return binary.BigEndian.Uint64(b[:]), err
}

func bytesValue(r io.Reader, e element, limit uint64) ([]byte, error) {
	if e.size > limit {
		return nil, fmt.Errorf("oversized subtitle element")
	}
	b := make([]byte, int(e.size))
	_, err := io.ReadFull(r, b)
	return b, err
}

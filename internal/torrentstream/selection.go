package torrentstream

import (
	"context"
	"fmt"
	"strings"
)

// SelectMedia selects an explicit torrent index, or the only media file for
// index -1. Multi-file CLI inputs list choices instead of picking arbitrarily.
func (s *Session) SelectMedia(ctx context.Context, index int) (string, error) {
	files, err := s.Files(ctx)
	if err != nil {
		return "", err
	}
	if index >= 0 {
		return s.Select(index)
	}
	if index != -1 {
		return "", fmt.Errorf("invalid torrent file index %d", index)
	}
	if len(files) == 1 {
		return s.Select(files[0].Index)
	}
	var choices strings.Builder
	choices.WriteString("choose a media file with -torrent-file INDEX:")
	for _, file := range files {
		fmt.Fprintf(&choices, "\n  %d: %s (%.1f MiB)", file.Index, file.Name, float64(file.Size)/(1<<20))
	}
	return "", fmt.Errorf("%s", choices.String())
}

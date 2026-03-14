package playlists

import (
	"fmt"
	"gopkg.in/ini.v1"
	"path/filepath"
	"strings"
)

type Iterator interface {
	HasNext() bool
	Next() (file, title string)
}

func IsPlaylist(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".m3u" || ext == ".pls"
}

// NewPlaylistIterator creates an iterator for the given playlist.
func NewIterator(uri string) (Iterator, error) {
	ext := strings.ToLower(filepath.Ext(uri))
	switch ext {
	case ".pls":
		return newPLSIterator(uri)
	case ".m3u":
		return newM3UIterator(uri)
	}
	return nil, fmt.Errorf("'%v' is not a recognized playlist format", ext)
}

// plsIterator is an iterator for playlist-files.
// According to https://en.wikipedia.org/wiki/PLS_(file_format),
// The format is case-sensitive and essentially that of an INI file.
// It has entries on the form File1, Title1 etc.
type plsIterator struct {
	count    int
	playlist *ini.Section
}

func newPLSIterator(uri string) (*plsIterator, error) {
	content, err := FetchResource(uri)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %v: %w", uri, err)
	}
	pls, err := ini.LoadSources(ini.LoadOptions{IgnoreInlineComment: true}, content)
	if err != nil {
		return nil, fmt.Errorf("failed to parse file %v: %w", uri, err)
	}
	section, err := pls.GetSection("playlist")
	if err != nil {
		return nil, fmt.Errorf("failed to find playlist in .pls-file %v", uri)
	}
	return &plsIterator{
		playlist: section,
	}, nil
}

func (it *plsIterator) HasNext() bool {
	return it.playlist.HasKey(fmt.Sprintf("File%d", it.count+1))
}

func (it *plsIterator) Next() (file, title string) {
	if val := it.playlist.Key(fmt.Sprintf("File%d", it.count+1)); val != nil {
		file = val.Value()
	}
	if val := it.playlist.Key(fmt.Sprintf("Title%d", it.count+1)); val != nil {
		title = val.Value()
	}
	it.count = it.count + 1
	return file, title
}

// m3uIterator is an iterator for m3u-files.
// https://docs.fileformat.com/audio/m3u/:
//
// There is no official specification for the M3U file format, it is a de-facto standard.
// M3U is a plain text file that uses the .m3u extension if the text is encoded
// in the local system’s default non-Unicode encoding or with the .m3u8 extension
// if the text is UTF-8 encoded. Each entry in the M3U file can be one of the following:
//
//   - Absolute path to the file
//   - File path relative to the M3U file.
//   - URL
//
// In the extended M3U, additional directives are introduced that begin
// with “#” and end with a colon(:) if they have parameters
type m3uIterator struct {
	index int
	lines []string
}

func newM3UIterator(uri string) (*m3uIterator, error) {
	content, err := FetchResource(uri)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %v: %w", uri, err)
	}
	var lines []string
	// convert windows linebreaks, and split
	for l := range strings.SplitSeq(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n") {
		// This is a very simple m3u decoder, ignores all extended info
		l = strings.TrimSpace(l)
		if len(l) > 0 && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	return &m3uIterator{
		index: 0,
		lines: lines,
	}, nil
}

func (it *m3uIterator) HasNext() bool {
	return it.index < len(it.lines)
}

func (it *m3uIterator) Next() (file, title string) {
	file = it.lines[it.index]
	title = "" // Todo?
	it.index++
	return file, title
}

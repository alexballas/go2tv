package playlists

import (
	"io"
	"net/http"
	"os"
	"strings"
)

// FetchResource fetches the given url and returns the response body. The url can either
// be an HTTP url or a file:// url.
func FetchResource(url string) ([]byte, error) {
	if filep, ok := strings.CutPrefix(url, "file://"); ok {
		return os.ReadFile(filep)
	}
	res, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}

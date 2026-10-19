package radiobrowser

import (
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	_ "github.com/fyne-io/image/ico"
	_ "golang.org/x/image/webp"
	"golang.org/x/net/html"

	"go2tv.app/go2tv/v2/metadata"
)

const (
	MaxIconBytes     = 2 << 20
	maxIconPixels    = 16_000_000
	maxHomepageBytes = 1 << 20
	iconTimeout      = 5 * time.Second
	// Covers the favicon, the homepage and the icon it declares.
	iconFetchTimeout = 3 * iconTimeout
)

// ErrNoIcon reports a station without a favicon or homepage.
var ErrNoIcon = errors.New("radio icon: no source")

var iconHTTPClient = &http.Client{
	Timeout: iconTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("radio icon: too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("radio icon: invalid redirect")
		}
		return nil
	},
}

// IconSource locates a station logo: the directory favicon, else the icon
// declared by the station homepage. Many stations list no favicon, or a dead
// one, while their homepage still links a logo.
type IconSource struct {
	Favicon  string
	Homepage string
}

// Icon returns the station's logo source; only HTTP(S) homepages are kept.
func (s Station) Icon() IconSource {
	source := IconSource{Favicon: strings.TrimSpace(s.Favicon)}
	if homepage, err := url.Parse(strings.TrimSpace(s.Homepage)); err == nil && homepage.Host != "" && (homepage.Scheme == "http" || homepage.Scheme == "https") {
		source.Homepage = homepage.String()
	}
	return source
}

func (s IconSource) Empty() bool { return s.Favicon == "" && s.Homepage == "" }

// Key identifies the source for caches.
func (s IconSource) Key() string { return s.Favicon + "\x00" + s.Homepage }

// FetchIcon downloads a station logo and normalizes it to renderer artwork,
// falling back to the homepage's declared icon when the favicon fails.
func FetchIcon(ctx context.Context, source IconSource) (*metadata.ArtworkAsset, error) {
	if source.Empty() {
		return nil, ErrNoIcon
	}
	ctx, cancel := context.WithTimeout(ctx, iconFetchTimeout)
	defer cancel()
	var errs []error
	if source.Favicon != "" {
		asset, err := fetchIconURL(ctx, source.Favicon)
		if err == nil {
			return asset, nil
		}
		errs = append(errs, err)
	}
	if source.Homepage != "" && ctx.Err() == nil {
		iconURL, err := homepageIcon(ctx, source.Homepage)
		switch {
		case err != nil:
			errs = append(errs, err)
		case iconURL == source.Favicon:
		default:
			asset, err := fetchIconURL(ctx, iconURL)
			if err == nil {
				return asset, nil
			}
			errs = append(errs, err)
		}
	}
	// A canceled homepage-only fetch has no error of its own.
	return nil, cmp.Or(errors.Join(errs...), ctx.Err(), ErrNoIcon)
}

func iconGet(ctx context.Context, rawURL string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("radio icon request: %w", err)
	}
	request.Header = http.Header{"User-Agent": {"Go2TV"}, "Accept": {"image/*, text/html;q=0.9, */*;q=0.8"}}
	response, err := iconHTTPClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("radio icon fetch: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("radio icon status: %d", response.StatusCode)
	}
	return response, nil
}

// fetchIconURL converts GIF, WebP and ICO logos, since artwork normalization
// accepts only JPEG and PNG input.
func fetchIconURL(ctx context.Context, rawURL string) (*metadata.ArtworkAsset, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("radio icon: invalid URL")
	}
	response, err := iconGet(ctx, parsed.String())
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxIconBytes+1))
	if err != nil {
		return nil, fmt.Errorf("radio icon body: %w", err)
	}
	if len(data) > MaxIconBytes {
		return nil, fmt.Errorf("radio icon too large")
	}
	if err := checkICO(data); err != nil {
		return nil, err
	}
	config, format, err := decodeConfig(data)
	if err != nil {
		return nil, fmt.Errorf("radio icon decode: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || uint64(config.Width)*uint64(config.Height) > maxIconPixels {
		return nil, fmt.Errorf("radio icon dimensions too large")
	}
	switch format {
	case "jpeg", "png":
	case "gif", "webp", "ico":
		decoded, err := decodeImage(data)
		if err != nil {
			return nil, fmt.Errorf("radio icon decode: %w", err)
		}
		var converted bytes.Buffer
		if err := png.Encode(&converted, decoded); err != nil {
			return nil, fmt.Errorf("radio icon convert: %w", err)
		}
		data = converted.Bytes()
	default:
		return nil, fmt.Errorf("radio icon: unsupported image")
	}
	asset, err := metadata.LoadArtwork(data, parsed.String())
	if err != nil {
		return nil, fmt.Errorf("radio icon normalize: %w", err)
	}
	return asset, nil
}

// checkICO bounds ICO directories before decoding: the decoder allocates each
// entry's declared size and slices entries without bounds checks.
func checkICO(data []byte) error {
	if !bytes.HasPrefix(data, []byte{0, 0, 1, 0}) {
		return nil
	}
	if len(data) < 6 {
		return fmt.Errorf("radio icon: invalid ICO")
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	directory := 6 + 16*count
	if count == 0 || directory > len(data) {
		return fmt.Errorf("radio icon: invalid ICO")
	}
	total := uint64(directory)
	for i := range count {
		total += uint64(binary.LittleEndian.Uint32(data[6+16*i+8:]))
		if total > uint64(len(data)) {
			return fmt.Errorf("radio icon: invalid ICO")
		}
	}
	return nil
}

// Third-party decoders (ICO) can panic on malformed remote input.
func decodeConfig(data []byte) (config image.Config, format string, err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("radio icon: malformed image")
		}
	}()
	return image.DecodeConfig(bytes.NewReader(data))
}

func decodeImage(data []byte) (decoded image.Image, err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("radio icon: malformed image")
		}
	}()
	decoded, _, err = image.Decode(bytes.NewReader(data))
	return decoded, err
}

// homepageIcon resolves the best icon a homepage declares, else /favicon.ico.
func homepageIcon(ctx context.Context, homepage string) (string, error) {
	response, err := iconGet(ctx, homepage)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	href := declaredIcon(io.LimitReader(response.Body, maxHomepageBytes))
	if href == "" {
		href = "/favicon.ico"
	}
	reference, err := url.Parse(href)
	if err != nil {
		return "", fmt.Errorf("radio icon: invalid homepage icon: %w", err)
	}
	// Redirects move the base, e.g. http to https or to a station subpage.
	return response.Request.URL.ResolveReference(reference).String(), nil
}

// declaredIcon prefers PNG, then the largest declared size. SVG is skipped
// since it cannot be rasterized for renderers.
func declaredIcon(r io.Reader) string {
	tokenizer := html.NewTokenizer(r)
	var best string
	bestPNG, bestSize := false, -1
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return best
		case html.EndTagToken:
			if name, _ := tokenizer.TagName(); string(name) == "head" {
				return best
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			switch token.Data {
			case "body":
				return best
			case "link":
				href, isPNG, size, ok := iconLink(token)
				if ok && (isPNG && !bestPNG || isPNG == bestPNG && size > bestSize) {
					best, bestPNG, bestSize = href, isPNG, size
				}
			}
		}
	}
}

func iconLink(token html.Token) (href string, isPNG bool, size int, ok bool) {
	var mimeType string
	for _, attr := range token.Attr {
		switch attr.Key {
		case "rel":
			for relation := range strings.FieldsSeq(strings.ToLower(attr.Val)) {
				if relation == "icon" || relation == "apple-touch-icon" {
					ok = true
				}
			}
		case "href":
			href = strings.TrimSpace(attr.Val)
		case "sizes":
			if width, _, found := strings.Cut(strings.ToLower(attr.Val), "x"); found {
				size, _ = strconv.Atoi(width)
			}
		case "type":
			mimeType = strings.ToLower(strings.TrimSpace(attr.Val))
		}
	}
	extension := ""
	if reference, err := url.Parse(href); err == nil {
		extension = strings.ToLower(path.Ext(reference.Path))
	}
	if href == "" || strings.HasPrefix(strings.ToLower(href), "data:") || mimeType == "image/svg+xml" || extension == ".svg" {
		return "", false, 0, false
	}
	return href, mimeType == "image/png" || extension == ".png", size, ok
}

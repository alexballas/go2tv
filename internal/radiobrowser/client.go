// Package radiobrowser discovers internet radio stations using Radio Browser.
package radiobrowser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

const (
	requestTimeout   = 15 * time.Second
	maxResponseBytes = 2 << 20
	DefaultLimit     = 100

	// Radio Browser often runs a single server, so transient failures are
	// retried there too, backing off retryDelay then 3×retryDelay.
	minAttempts = 3
	retryDelay  = 500 * time.Millisecond

	// The directory changes slowly; caching spares Radio Browser repeat
	// requests from dialog reopens and multiple WebUI remotes.
	searchCacheTTL    = 5 * time.Minute
	countriesCacheTTL = 6 * time.Hour
	maxSearchCache    = 50
)

var ErrHTTPStatus = errors.New("radio browser HTTP status")

// fallbackMirrors follow discovered servers so a DNS hiccup or a stale
// reverse record never leaves the client without a known endpoint.
var fallbackMirrors = []string{"https://de1.api.radio-browser.info", "https://de2.api.radio-browser.info", "https://all.api.radio-browser.info"}

type httpStatusError int

func (e httpStatusError) Error() string        { return fmt.Sprintf("%v: %d", ErrHTTPStatus, int(e)) }
func (e httpStatusError) Is(target error) bool { return target == ErrHTTPStatus }

type Station struct {
	UUID        string `json:"stationuuid"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	ResolvedURL string `json:"url_resolved"`
	Homepage    string `json:"homepage"`
	Favicon     string `json:"favicon"`
	Country     string `json:"country"`
	CountryCode string `json:"countrycode"`
	Language    string `json:"language"`
	Tags        string `json:"tags"`
	Codec       string `json:"codec"`
	Bitrate     int    `json:"bitrate"`
	HLS         int    `json:"hls"`
}

func (s Station) StreamURL() string {
	for _, value := range []string{s.ResolvedURL, s.URL} {
		value = strings.TrimSpace(value)
		u, err := url.Parse(value)
		if err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
			return value
		}
	}
	return ""
}

func (s Station) MediaType() string {
	switch strings.ToUpper(strings.TrimSpace(s.Codec)) {
	case "AAC", "AAC+":
		return "audio/aac"
	case "OGG", "OPUS", "VORBIS":
		return "audio/ogg"
	case "FLAC":
		return "audio/flac"
	default:
		return "audio/mpeg"
	}
}

type Country struct {
	Name         string `json:"name"`
	Code         string `json:"iso_3166_1"`
	StationCount int    `json:"stationcount"`
}

type Query struct {
	Name        string
	CountryCode string
	Tag         string
	Limit       int
}

type cachedSearch struct {
	stations []Station
	expires  time.Time
}

// Client caches mirror discovery, searches and countries, and retries failed
// requests on another server. A single instance may be used by concurrent
// directory requests.
type Client struct {
	http        *http.Client
	userAgent   string
	now         func() time.Time
	discover    func(context.Context) []string
	retryDelay  time.Duration
	mu          sync.Mutex
	mirrors     []string
	discovered  bool
	cacheMu     sync.Mutex
	searches    map[string]cachedSearch
	searchOrder []string
	countries   []Country
	countriesAt time.Time
}

func New(userAgent string) *Client {
	return &Client{http: &http.Client{Timeout: requestTimeout}, userAgent: userAgent, now: time.Now, discover: discoverMirrors, retryDelay: retryDelay}
}

// cachedSearch returns a stored search and whether it is still fresh.
func (c *Client) cachedSearch(key string) ([]Station, bool, bool) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	entry, ok := c.searches[key]
	if !ok {
		return nil, false, false
	}
	return slices.Clone(entry.stations), true, c.now().Before(entry.expires)
}

func (c *Client) storeSearch(key string, stations []Station) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	if c.searches == nil {
		c.searches = make(map[string]cachedSearch)
	}
	if _, exists := c.searches[key]; !exists {
		if len(c.searchOrder) >= maxSearchCache {
			delete(c.searches, c.searchOrder[0])
			c.searchOrder = c.searchOrder[1:]
		}
		c.searchOrder = append(c.searchOrder, key)
	}
	c.searches[key] = cachedSearch{stations: slices.Clone(stations), expires: c.now().Add(searchCacheTTL)}
}

func (c *Client) servers(ctx context.Context) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.discovered {
		c.mirrors = c.discover(ctx)
		c.discovered = true
	}
	return append([]string(nil), c.mirrors...)
}

// rediscover forgets the mirror list after every server failed, so the next
// request looks up the current Radio Browser servers again.
func (c *Client) rediscover() {
	c.mu.Lock()
	c.discovered = false
	c.mu.Unlock()
}

// discoverMirrors resolves all.api.radio-browser.info and names each address
// by reverse lookup, as Radio Browser recommends.
func discoverMirrors(ctx context.Context) []string {
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var mirrors []string
	addresses, err := net.DefaultResolver.LookupHost(lookupCtx, "all.api.radio-browser.info")
	if err == nil {
		for _, address := range addresses {
			hosts, lookupErr := net.DefaultResolver.LookupAddr(lookupCtx, address)
			if lookupErr != nil {
				continue
			}
			for _, host := range hosts {
				name := strings.TrimSuffix(host, ".")
				if strings.HasSuffix(name, ".api.radio-browser.info") {
					mirrors = append(mirrors, "https://"+name)
				}
			}
		}
	}
	rand.Shuffle(len(mirrors), func(i, j int) { mirrors[i], mirrors[j] = mirrors[j], mirrors[i] })
	return withFallbackMirrors(mirrors)
}

// withFallbackMirrors keeps discovered servers first, then adds every
// fallback not already present.
func withFallbackMirrors(discovered []string) []string {
	mirrors := make([]string, 0, len(discovered)+len(fallbackMirrors))
	for _, mirror := range slices.Concat(discovered, fallbackMirrors) {
		if !slices.Contains(mirrors, mirror) {
			mirrors = append(mirrors, mirror)
		}
	}
	return mirrors
}

func (c *Client) Search(ctx context.Context, q Query) ([]Station, error) {
	limit := q.Limit
	if limit <= 0 || limit > DefaultLimit {
		limit = DefaultLimit
	}
	values := url.Values{"hidebroken": {"true"}, "order": {"clickcount"}, "reverse": {"true"}, "limit": {strconv.Itoa(limit)}}
	if name := strings.TrimSpace(q.Name); name != "" {
		values.Set("name", name)
	}
	if code := strings.ToUpper(strings.TrimSpace(q.CountryCode)); code != "" {
		values.Set("countrycode", code)
	}
	if tag := strings.TrimSpace(q.Tag); tag != "" {
		values.Set("tag", tag)
	}
	key := values.Encode()
	cached, ok, fresh := c.cachedSearch(key)
	if ok && fresh {
		return cached, nil
	}
	var stations []Station
	if err := c.getJSON(ctx, "/json/stations/search", values, &stations); err != nil {
		// An unreachable directory still serves the last results for this search.
		if ok && ctx.Err() == nil {
			return cached, nil
		}
		return nil, err
	}
	result := make([]Station, 0, len(stations))
	seen := make(map[string]bool)
	for _, station := range stations {
		station.UUID = strings.TrimSpace(station.UUID)
		station.Name = strings.TrimSpace(station.Name)
		if station.UUID == "" || station.Name == "" || station.StreamURL() == "" || seen[station.UUID] {
			continue
		}
		seen[station.UUID] = true
		result = append(result, station)
		if len(result) == limit {
			break
		}
	}
	c.storeSearch(key, result)
	return result, nil
}

// SearchText searches station names and genre tags with one user query.
// Results alternate between both rankings and retain each UUID once.
func (c *Client) SearchText(ctx context.Context, text, countryCode string) ([]Station, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return c.Search(ctx, Query{CountryCode: countryCode})
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	type result struct {
		group    int
		stations []Station
		err      error
	}
	results := make(chan result, 2)
	queries := []Query{{Name: text, CountryCode: countryCode}, {Tag: text, CountryCode: countryCode}}
	for group, query := range queries {
		go func() {
			stations, err := c.Search(ctx, query)
			results <- result{group: group, stations: stations, err: err}
		}()
	}
	groups := make([][]Station, 2)
	var lastErr error
	collect := func(result result) {
		groups[result.group] = result.stations
		if result.err != nil {
			lastErr = result.err
		}
	}
waiting:
	for range 2 {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil, ctx.Err()
			}
			lastErr = ctx.Err()
			// A completed query may still be buffered when the other query
			// reaches the deadline. Keep all results already available.
			for {
				select {
				case result := <-results:
					collect(result)
				default:
					break waiting
				}
			}
		case result := <-results:
			collect(result)
		}
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, ctx.Err()
	}
	stations := make([]Station, 0, DefaultLimit)
	seen := make(map[string]bool)
	for rank := 0; rank < max(len(groups[0]), len(groups[1])) && len(stations) < DefaultLimit; rank++ {
		for _, group := range groups {
			if rank >= len(group) || seen[group[rank].UUID] {
				continue
			}
			station := group[rank]
			seen[station.UUID] = true
			stations = append(stations, station)
			if len(stations) == DefaultLimit {
				break
			}
		}
	}
	if len(stations) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return stations, nil
}

// Countries serves the cached list while fresh, and a stale one when the
// directory is unreachable.
func (c *Client) Countries(ctx context.Context) ([]Country, error) {
	c.cacheMu.Lock()
	cached, fresh := c.countries, c.now().Sub(c.countriesAt) < countriesCacheTTL
	c.cacheMu.Unlock()
	if cached != nil && fresh {
		return slices.Clone(cached), nil
	}
	result, err := c.fetchCountries(ctx)
	if err != nil {
		if cached != nil && ctx.Err() == nil {
			return slices.Clone(cached), nil
		}
		return nil, err
	}
	c.cacheMu.Lock()
	c.countries, c.countriesAt = result, c.now()
	c.cacheMu.Unlock()
	return slices.Clone(result), nil
}

func (c *Client) fetchCountries(ctx context.Context) ([]Country, error) {
	var records []Country
	if err := c.getJSON(ctx, "/json/countries", url.Values{"hidebroken": {"true"}}, &records); err != nil {
		return nil, err
	}
	result := make([]Country, 0, len(records))
	seen := make(map[string]bool)
	for _, country := range records {
		country.Code = strings.ToUpper(strings.TrimSpace(country.Code))
		if len(country.Code) != 2 || seen[country.Code] {
			continue
		}
		region, err := language.ParseRegion(country.Code)
		if err != nil {
			continue
		}
		country.Name = display.English.Regions().Name(region)
		if country.Name == "" {
			country.Name = country.Code
		}
		seen[country.Code] = true
		result = append(result, country)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// Station refreshes a saved favorite by its stable directory UUID.
func (c *Client) Station(ctx context.Context, uuid string) (Station, error) {
	var stations []Station
	if err := c.getJSON(ctx, "/json/stations/byuuid/"+url.PathEscape(uuid), nil, &stations); err != nil {
		return Station{}, err
	}
	for _, station := range stations {
		if station.UUID == uuid && station.StreamURL() != "" {
			return station, nil
		}
	}
	return Station{}, errors.New("radio browser: station unavailable")
}

// Click reports a cast selection. Reporting failures never prevent playback.
func (c *Client) Click(ctx context.Context, uuid string) error {
	if strings.TrimSpace(uuid) == "" {
		return errors.New("radio browser: empty station UUID")
	}
	var response struct {
		OK json.RawMessage `json:"ok"`
	}
	if err := c.getJSON(ctx, "/json/url/"+url.PathEscape(uuid), nil, &response); err != nil {
		return err
	}
	if string(response.OK) != "true" && string(response.OK) != `"true"` {
		return errors.New("radio browser: click rejected")
	}
	return nil
}

func (c *Client) getJSON(ctx context.Context, path string, query url.Values, target any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	servers := c.servers(ctx)
	if len(servers) == 0 {
		return errors.New("radio browser: no servers")
	}
	var lastErr error
	for attempt := range max(len(servers), minAttempts) {
		if attempt > 0 && retryable(lastErr) {
			if err := c.backoff(ctx, attempt); err != nil {
				return err
			}
		} else if attempt >= len(servers) {
			// Revisit servers only for failures that may clear up.
			break
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		server := servers[attempt%len(servers)]
		// Reserve time for failover within the overall request deadline.
		attemptCtx, attemptCancel := context.WithTimeout(ctx, 5*time.Second)
		err := c.request(attemptCtx, server+path+"?"+query.Encode(), target)
		attemptCancel()
		if err == nil {
			c.mu.Lock()
			for i, mirror := range c.mirrors {
				if mirror == server {
					c.mirrors[0], c.mirrors[i] = c.mirrors[i], c.mirrors[0]
					break
				}
			}
			c.mu.Unlock()
			return nil
		}
		lastErr = err
	}
	c.rediscover()
	return fmt.Errorf("radio browser: %w", lastErr)
}

// backoff waits before retrying a transient failure: retryDelay after the
// first attempt, then 3×retryDelay.
func (c *Client) backoff(ctx context.Context, attempt int) error {
	delay := c.retryDelay
	if attempt > 1 {
		delay *= 3
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// retryable reports failures that may clear up on a retry: network errors,
// timeouts, rate limiting and server errors.
func retryable(err error) bool {
	if status, ok := errors.AsType[httpStatusError](err); ok {
		return status == http.StatusTooManyRequests || status >= 500
	}
	var netErr net.Error
	return errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded)
}

func (c *Client) request(ctx context.Context, endpoint string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("radio browser request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return httpStatusError(resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("radio browser response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return errors.New("radio browser response too large")
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("radio browser JSON: %w", err)
	}
	return nil
}

package radiobrowser

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(servers ...string) *Client {
	return &Client{
		http: http.DefaultClient, userAgent: "Go2TV/test", now: time.Now, retryDelay: time.Millisecond,
		mirrors: servers, discovered: true,
		discover: func(context.Context) []string { return slices.Clone(servers) },
	}
}

func TestSearchFiltersAndPlayableStations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/stations/search" || r.Header.Get("User-Agent") != "Go2TV/test" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("unexpected request: %s, %v", r.URL, r.Header)
		}
		for key, want := range map[string]string{"name": "rock & jazz", "countrycode": "GR", "tag": "r&b", "hidebroken": "true", "limit": "100", "order": "clickcount", "reverse": "true"} {
			if got := r.URL.Query().Get(key); got != want {
				t.Errorf("%s = %q, want %q", key, got, want)
			}
		}
		fmt.Fprint(w, `[{"stationuuid":"one","name":" Station ","favicon":"https://station.test/icon.png","url":"https://station.test/list.pls","url_resolved":"https://station.test/live"},{"stationuuid":"one","name":"duplicate","url":"https://station.test/other"},{"stationuuid":"two","name":"Fallback","url":"https://station.test/fallback","url_resolved":"file:///tmp/file"},{"stationuuid":"bad","name":"Unsafe","url":"file:///tmp/file"},{"name":"No ID","url":"https://station.test/none"}]`)
	}))
	defer server.Close()
	stations, err := testClient(server.URL).Search(context.Background(), Query{Name: " rock & jazz ", CountryCode: "gr", Tag: "r&b", Limit: 10000})
	if err != nil {
		t.Fatal(err)
	}
	if len(stations) != 2 || stations[0].Favicon != "https://station.test/icon.png" || stations[0].Name != "Station" || stations[0].StreamURL() != "https://station.test/live" || stations[1].StreamURL() != "https://station.test/fallback" {
		t.Fatalf("stations = %#v", stations)
	}
}

func TestMirrorFailover(t *testing.T) {
	tt := []struct {
		name   string
		status int
		body   string
	}{
		{name: "unavailable", status: 503},
		{name: "invalid response", status: 200, body: "not JSON"},
		{name: "oversized response", status: 200, body: strings.Repeat(" ", maxResponseBytes+1)},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer bad.Close()
			good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `[{"stationuuid":"one","name":"Station","url_resolved":"https://station.test/live"}]`)
			}))
			defer good.Close()
			client := testClient(bad.URL, good.URL)
			stations, err := client.Search(context.Background(), Query{})
			if err != nil || len(stations) != 1 {
				t.Fatalf("failover: %#v, %v", stations, err)
			}
			if client.servers(context.Background())[0] != good.URL {
				t.Fatal("successful mirror not preferred")
			}
		})
	}
}

func TestCanceledSearchDoesNotContactMirrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("canceled search contacted server") }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := testClient(server.URL).Search(ctx, Query{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestCountriesUseStableCodesAndReadableNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"name":"Deutschland","iso_3166_1":"de"},{"name":"duplicate","iso_3166_1":"DE"},{"name":"Greece","iso_3166_1":"GR"},{"name":"bad"}]`)
	}))
	defer server.Close()
	countries, err := testClient(server.URL).Countries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Country{{Name: "Germany", Code: "DE"}, {Name: "Greece", Code: "GR"}}
	if !reflect.DeepEqual(countries, want) {
		t.Fatalf("countries = %#v", countries)
	}
}

func TestClickAcceptsAPIBooleanForms(t *testing.T) {
	for _, value := range []string{"true", `"true"`, "false"} {
		t.Run(value, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/json/url/station-id" {
					t.Errorf("path = %q", r.URL.Path)
				}
				fmt.Fprintf(w, `{"ok":%s}`, value)
			}))
			defer server.Close()
			err := testClient(server.URL).Click(context.Background(), "station-id")
			if (err != nil) != (value == "false") {
				t.Fatalf("click error = %v", err)
			}
		})
	}
}

func TestFavoriteRefreshUsesUUID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/stations/byuuid/saved-station" {
			t.Errorf("favorite refresh path = %s", r.URL.Path)
		}
		fmt.Fprint(w, `[{"stationuuid":"saved-station","name":"Renamed station","url_resolved":"https://station.test/new-stream"}]`)
	}))
	defer server.Close()
	station, err := testClient(server.URL).Station(context.Background(), "saved-station")
	if err != nil || station.StreamURL() != "https://station.test/new-stream" {
		t.Fatalf("favorite refresh: %#v, %v", station, err)
	}
}

func TestSingleSearchFindsNamesAndGenresWithoutDuplicates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("countrycode") != "GR" {
			t.Errorf("country lost: %v", query)
		}
		switch {
		case query.Get("name") == "jazz" && query.Get("tag") == "":
			fmt.Fprint(w, `[{"stationuuid":"name","name":"Jazz FM","url":"https://station.test/name"},{"stationuuid":"both","name":"Both","url":"https://station.test/both"}]`)
		case query.Get("tag") == "jazz" && query.Get("name") == "":
			fmt.Fprint(w, `[{"stationuuid":"genre","name":"Smooth Radio","url":"https://station.test/genre"},{"stationuuid":"both","name":"Both","url":"https://station.test/both"}]`)
		default:
			t.Errorf("invalid single search: %v", query)
			fmt.Fprint(w, `[]`)
		}
	}))
	defer server.Close()
	stations, err := testClient(server.URL).SearchText(context.Background(), " jazz ", "GR")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, station := range stations {
		ids = append(ids, station.UUID)
	}
	if !reflect.DeepEqual(ids, []string{"name", "genre", "both"}) {
		t.Fatalf("single search results = %v", ids)
	}
}

func TestSearchTextKeepsCompletedMatchesOnDeadline(t *testing.T) {
	tt := []struct {
		name     string
		fastName bool
		empty    bool
	}{
		{name: "name matches survive genre timeout", fastName: true},
		{name: "genre matches survive name timeout"},
		{name: "no usable matches returns timeout", fastName: true, empty: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if (r.URL.Query().Get("name") != "") != tc.fastName {
					<-r.Context().Done()
					return
				}
				if tc.empty {
					fmt.Fprint(w, `[]`)
					return
				}
				fmt.Fprint(w, `[{"stationuuid":"matched","name":"Jazz","url":"https://station.test/live"}]`)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
			defer cancel()
			stations, err := testClient(server.URL).SearchText(ctx, "jazz", "GR")
			if tc.empty {
				if !errors.Is(err, context.DeadlineExceeded) || len(stations) != 0 {
					t.Fatalf("empty timed-out search = %#v, %v", stations, err)
				}
				return
			}
			if err != nil || len(stations) != 1 || stations[0].UUID != "matched" {
				t.Fatalf("completed matches discarded on timeout: %#v, %v", stations, err)
			}
		})
	}
}

func TestSearchTextCancellationDiscardsPartialMatches(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "" {
			fmt.Fprint(w, `[{"stationuuid":"matched","name":"Jazz","url":"https://station.test/live"}]`)
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	stop := time.AfterFunc(150*time.Millisecond, cancel)
	defer stop.Stop()
	stations, err := testClient(server.URL).SearchText(ctx, "jazz", "GR")
	if !errors.Is(err, context.Canceled) || len(stations) != 0 {
		t.Fatalf("canceled search published stale matches: %#v, %v", stations, err)
	}
}

func TestDirectoryCacheSparesRepeatRequests(t *testing.T) {
	tt := []struct {
		name  string
		fetch func(*Client) error
		ttl   time.Duration
	}{
		{name: "search", ttl: searchCacheTTL, fetch: func(c *Client) error {
			_, err := c.SearchText(context.Background(), "jazz", "GR")
			return err
		}},
		{name: "countries", ttl: countriesCacheTTL, fetch: func(c *Client) error {
			_, err := c.Countries(context.Background())
			return err
		}},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			var fail atomic.Bool
			fail.Store(true)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if fail.Load() {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				fmt.Fprint(w, `[{"stationuuid":"one","name":"Station","url_resolved":"https://station.test/live","iso_3166_1":"GR"}]`)
			}))
			defer server.Close()
			now := time.Now()
			client := testClient(server.URL)
			client.now = func() time.Time { return now }

			if err := tc.fetch(client); err == nil {
				t.Fatal("upstream failure not reported")
			}
			fail.Store(false)
			if err := tc.fetch(client); err != nil {
				t.Fatalf("failure was cached: %v", err)
			}
			fetched := requests.Load()
			if err := tc.fetch(client); err != nil || requests.Load() != fetched {
				t.Fatalf("repeat fetched upstream: %d -> %d, %v", fetched, requests.Load(), err)
			}
			now = now.Add(tc.ttl)
			if err := tc.fetch(client); err != nil || requests.Load() == fetched {
				t.Fatalf("expired entry not refetched: %v", err)
			}
		})
	}
}

func TestCountriesServeStaleListWhenDirectoryFails(t *testing.T) {
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `[{"iso_3166_1":"GR"}]`)
	}))
	defer server.Close()
	now := time.Now()
	client := testClient(server.URL)
	client.now = func() time.Time { return now }
	if _, err := client.Countries(context.Background()); err != nil {
		t.Fatal(err)
	}
	fail, now = true, now.Add(countriesCacheTTL)
	countries, err := client.Countries(context.Background())
	if err != nil || len(countries) != 1 || countries[0].Code != "GR" {
		t.Fatalf("stale countries = %#v, %v", countries, err)
	}
}

func TestSingleMirrorRetriesTransientFailures(t *testing.T) {
	tt := []struct {
		name     string
		status   int
		requests int32
		ok       bool
	}{
		{name: "server error recovers", status: http.StatusServiceUnavailable, requests: 2, ok: true},
		{name: "rate limit recovers", status: http.StatusTooManyRequests, requests: 2, ok: true},
		{name: "client error is final", status: http.StatusNotFound, requests: 1},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) == 1 {
					w.WriteHeader(tc.status)
					return
				}
				fmt.Fprint(w, `[{"stationuuid":"one","name":"Station","url_resolved":"https://station.test/live"}]`)
			}))
			defer server.Close()
			stations, err := testClient(server.URL).Search(context.Background(), Query{})
			if (err == nil) != tc.ok || requests.Load() != tc.requests || (tc.ok && len(stations) != 1) {
				t.Fatalf("stations=%#v err=%v requests=%d", stations, err, requests.Load())
			}
		})
	}
}

func TestMirrorsRediscoveredAfterEveryServerFails(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer dead.Close()
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"stationuuid":"one","name":"Station","url_resolved":"https://station.test/live"}]`)
	}))
	defer live.Close()
	client := testClient(dead.URL)
	client.discover = func(context.Context) []string { return []string{live.URL} }
	if _, err := client.Search(context.Background(), Query{Name: "first"}); err == nil {
		t.Fatal("dead mirror reported success")
	}
	if _, err := client.Search(context.Background(), Query{Name: "second"}); err != nil {
		t.Fatalf("mirror list not rediscovered: %v", err)
	}
}

func TestFallbackMirrorsFollowDiscoveredServers(t *testing.T) {
	discovered := []string{"https://xx1.api.radio-browser.info", "https://de1.api.radio-browser.info"}
	want := []string{"https://xx1.api.radio-browser.info", "https://de1.api.radio-browser.info", "https://de2.api.radio-browser.info", "https://all.api.radio-browser.info"}
	if got := withFallbackMirrors(discovered); !reflect.DeepEqual(got, want) {
		t.Fatalf("mirrors = %v", got)
	}
}

func TestSearchServesStaleResultsWhenDirectoryFails(t *testing.T) {
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `[{"stationuuid":"one","name":"Station","url_resolved":"https://station.test/live"}]`)
	}))
	defer server.Close()
	now := time.Now()
	client := testClient(server.URL)
	client.now = func() time.Time { return now }
	if _, err := client.Search(context.Background(), Query{CountryCode: "GR"}); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	now = now.Add(searchCacheTTL)
	stations, err := client.Search(context.Background(), Query{CountryCode: "GR"})
	if err != nil || len(stations) != 1 || stations[0].UUID != "one" {
		t.Fatalf("stale search = %#v, %v", stations, err)
	}
	if _, err := client.Search(context.Background(), Query{CountryCode: "DE"}); err == nil {
		t.Fatal("uncached search hid directory failure")
	}
}

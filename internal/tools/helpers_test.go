package tools_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/tio-plato/doctolib-mcp/internal/doctolib"
	"github.com/tio-plato/doctolib-mcp/internal/gate"
	"github.com/tio-plato/doctolib-mcp/internal/place"
	"github.com/tio-plato/doctolib-mcp/internal/tools"
)

const (
	searchPath       = "/patient-health-search/api/v1/hcp/search"
	profilePath      = "/online_booking/api/slot_selection_funnel/v1/info.json"
	availabilityPath = "/availabilities.json"
	geoPath          = "/communes"
	testChrome       = "321"
)

// testNow is 00:30 on 17 November in Paris but still 16 November in UTC.
func testNow() time.Time {
	return time.Date(2026, 11, 16, 23, 30, 0, 0, time.UTC)
}

// request is what the fake server received.
type request struct {
	Method   string
	Path     string
	RawQuery string
	Header   http.Header
	Body     []byte
}

// fake stands in for both Doctolib and geo.api.gouv.fr on one loopback server.
type fake struct {
	mu       sync.Mutex
	requests []request
	handlers map[string]http.HandlerFunc
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, request{
		Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, Header: r.Header.Clone(), Body: body,
	})
	h := f.handlers[r.URL.Path]
	f.mu.Unlock()
	if h == nil {
		http.NotFound(w, r)
		return
	}
	h(w, r)
}

// handle sets how the fake answers path.
func (f *fake) handle(path string, h http.HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[path] = h
}

// doctolibRequests are the requests that went to Doctolib, not to the geocoder.
func (f *fake) doctolibRequests() []request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.DeleteFunc(slices.Clone(f.requests), func(r request) bool { return r.Path == geoPath })
}

func (f *fake) geoRequests() []request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.DeleteFunc(slices.Clone(f.requests), func(r request) bool { return r.Path != geoPath })
}

type env struct {
	tools *tools.Tools
	fake  *fake
	gate  *gate.Gate
	base  string
}

// newEnv starts the fake with normal answers: the 75001 arrondissement, the
// search, the profile and an availability with slots. The gate has no
// interval; configure may change its caps.
func newEnv(t *testing.T, configure ...func(*gate.Config)) *env {
	t.Helper()
	f := &fake{handlers: map[string]http.HandlerFunc{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	f.handle(geoPath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("codePostal") == "75001" && r.URL.Query().Get("type") == "arrondissement-municipal" {
			writeFile(t, w, "geo_75001.json")
			return
		}
		writeJSON(w, http.StatusOK, "[]")
	})
	f.handle(searchPath, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusPartialContent, string(readFile(t, "search.json")))
	})
	f.handle(profilePath, func(w http.ResponseWriter, _ *http.Request) {
		writeFile(t, w, "profile.json")
	})
	f.handle(availabilityPath, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, availabilityWithSlots)
	})

	cfg := gate.Config{
		Dir: t.TempDir(), Interval: 0, Hourly: gate.DefaultHourly, Daily: gate.DefaultDaily, Now: nil, Sleep: nil,
	}
	for _, c := range configure {
		c(&cfg)
	}
	g := gate.New(cfg)
	client := doctolib.New(doctolib.Config{BaseURL: srv.URL, ChromeMajor: testChrome, Gate: g, Timeout: 0})
	tl, err := tools.New(client, place.NewResolver(srv.URL), testNow)
	if err != nil {
		t.Fatal(err)
	}
	return &env{tools: tl, fake: f, gate: g, base: srv.URL}
}

const availabilityWithSlots = `{"total": 1, "availabilities": [{"date": "2026-11-17", "slots": ["2026-11-17T09:50:00.000+01:00"]}]}`

func readFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeFile(t *testing.T, w http.ResponseWriter, name string) {
	t.Helper()
	writeJSON(w, http.StatusOK, string(readFile(t, name)))
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func writeHTML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/html; charset=UTF-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func ptr[T any](v T) *T {
	return &v
}

// assertJSON compares got, once marshalled, with the JSON text want, ignoring formatting and key order.
func assertJSON(t *testing.T, got any, want string) {
	t.Helper()
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var g, w any
	err = json.Unmarshal(data, &g)
	if err != nil {
		t.Fatal(err)
	}
	err = json.Unmarshal([]byte(want), &w)
	if err != nil {
		t.Fatalf("bad expected JSON: %v", err)
	}
	if diff := cmp.Diff(w, g, cmpopts.EquateApprox(0, 1e-9)); diff != "" {
		t.Errorf("JSON mismatch (-want +got):\n%s", diff)
	}
}

// assertErrorPrefix checks that err is a tool error of the given category.
func assertErrorPrefix(t *testing.T, err error, category string) string {
	t.Helper()
	if err == nil {
		t.Fatalf("got no error, want a %q error", category)
	}
	text := tools.ErrorText(err)
	if !strings.HasPrefix(text, category+": ") {
		t.Fatalf("error %q does not start with %q", text, category+": ")
	}
	return text
}

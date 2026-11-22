package tools_test

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tio-plato/doctolib-mcp/internal/gate"
	"github.com/tio-plato/doctolib-mcp/internal/tools"
)

// blockPage is shaped like Doctolib's block page, with a made-up support code.
const blockPage = `<!DOCTYPE html><html><head><title>Doctolib</title><style>body{color:red}</style>
<script>var tracking = 1;</script></head><body><h1>Votre requête est bloquée !</h1>
<p>Le blocage de votre requête fait suite à un comportement ne respectant pas nos CGU.</p>
<p>Code pour le support: 0000fake0000</p></body></html>`

func gateNow() time.Time {
	return time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
}

func callAll(t *testing.T, e *env) []error {
	t.Helper()
	_, searchErr := e.tools.Search(t.Context(), tools.SearchInput{Query: "dentiste", Place: "75001"})
	_, profileErr := e.tools.Profile(
		t.Context(),
		tools.ProfileInput{ProfileURL: e.base + "/dentiste/paris/ada-example"},
	)
	_, availabilityErr := e.tools.Availability(t.Context(), tools.AvailabilityInput{
		Queries: []tools.AvailabilityQuery{query(3001, 1001)},
	})
	return []error{searchErr, profileErr, availabilityErr}
}

func TestBlockStopsEveryToolUntilTheMarkerIsDeleted(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(c *gate.Config) { c.Now = gateNow })
	e.fake.handle(availabilityPath, func(w http.ResponseWriter, _ *http.Request) {
		writeHTML(w, http.StatusForbidden, blockPage)
	})

	_, err := e.tools.Availability(t.Context(), tools.AvailabilityInput{
		Queries: []tools.AvailabilityQuery{query(3001, 1001)},
	})
	text := assertErrorPrefix(t, err, "blocked")
	for _, want := range []string{"Votre requête est bloquée !", "Code pour le support: 0000fake0000", e.gate.MarkerPath()} {
		if !strings.Contains(text, want) {
			t.Errorf("error %q lacks %q", text, want)
		}
	}
	if strings.Contains(text, "tracking") || strings.Contains(text, "color:red") {
		t.Errorf("error %q holds hidden page text", text)
	}
	data, err := os.ReadFile(e.gate.MarkerPath())
	if err != nil {
		t.Fatalf("no block marker: %v", err)
	}
	var marker gate.Marker
	err = json.Unmarshal(data, &marker)
	if err != nil || marker.Status != http.StatusForbidden || !marker.Time.Equal(gateNow()) ||
		marker.Path != availabilityPath || !strings.Contains(marker.Text, "0000fake0000") {
		t.Errorf("marker %s (%v)", data, err)
	}

	e.fake.handle(availabilityPath, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, availabilityWithSlots)
	})
	before := len(e.fake.doctolibRequests())
	for _, err := range callAll(t, e) {
		assertErrorPrefix(t, err, "blocked")
	}
	if after := len(e.fake.doctolibRequests()); after != before {
		t.Errorf("sent %d Doctolib requests while blocked", after-before)
	}

	err = os.Remove(e.gate.MarkerPath())
	if err != nil {
		t.Fatal(err)
	}
	for i, err := range callAll(t, e) {
		if err != nil {
			t.Errorf("tool %d after the marker was deleted: %v", i, err)
		}
	}
}

func TestBlockResponses(t *testing.T) {
	t.Parallel()
	for name, answer := range map[string]http.HandlerFunc{
		"403 JSON": func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusForbidden, `{}`) },
		"429":      func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusTooManyRequests, `{}`) },
		"200 HTML": func(w http.ResponseWriter, _ *http.Request) { writeHTML(w, http.StatusOK, blockPage) },
		"503 HTML": func(w http.ResponseWriter, _ *http.Request) { writeHTML(w, http.StatusServiceUnavailable, blockPage) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.fake.handle(searchPath, answer)

			_, err := e.tools.Search(t.Context(), tools.SearchInput{Query: "dentiste", Place: "75001"})
			assertErrorPrefix(t, err, "blocked")
			_, err = os.Stat(e.gate.MarkerPath())
			if err != nil {
				t.Errorf("no block marker: %v", err)
			}
		})
	}
}

func TestOtherFailuresAreErrorsWithoutMarkerNorRetry(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		status int
		answer http.HandlerFunc
	}{
		"404 HTML": {http.StatusNotFound, func(w http.ResponseWriter, _ *http.Request) {
			writeHTML(w, http.StatusNotFound, "<html><body>Page introuvable</body></html>")
		}},
		"520 HTML": {520, func(w http.ResponseWriter, _ *http.Request) {
			writeHTML(w, 520, "<html><body>Web server is returning an unknown error</body></html>")
		}},
		"302": {http.StatusFound, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		}},
		"500": {http.StatusInternalServerError, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusInternalServerError, `{"error": "internal"}`)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.fake.handle(profilePath, tc.answer)
			e.fake.handle(
				"/elsewhere",
				func(w http.ResponseWriter, _ *http.Request) { writeFile(t, w, "profile.json") },
			)

			_, err := e.tools.Profile(
				t.Context(),
				tools.ProfileInput{ProfileURL: e.base + "/dentiste/paris/ada-example"},
			)
			text := assertErrorPrefix(t, err, "error")
			if !strings.Contains(text, "HTTP "+strconv.Itoa(tc.status)) {
				t.Errorf("error %q lacks the HTTP status %d", text, tc.status)
			}
			_, err = os.Stat(e.gate.MarkerPath())
			if err == nil {
				t.Error("wrote a block marker")
			}
			if n := len(e.fake.doctolibRequests()); n != 1 {
				t.Errorf("sent %d requests, want 1", n)
			}
		})
	}
}

// Doctolib's profile endpoint answers 410 for a profile with no online booking, such as a search result with
// bookable_online false.
func TestProfileWithoutOnlineBooking(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	// The body of a real 410 was never kept: the server doesn't read it.
	e.fake.handle(profilePath, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusGone, `{}`)
	})

	_, err := e.tools.Profile(t.Context(), tools.ProfileInput{ProfileURL: e.base + "/dentiste/paris/ada-example"})
	assertErrorPrefix(t, err, "no_online_booking")
}

func TestCapsStopRequests(t *testing.T) {
	t.Parallel()
	for name, configure := range map[string]func(*gate.Config){
		"hourly": func(c *gate.Config) { c.Hourly = 2 },
		"daily":  func(c *gate.Config) { c.Daily = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, configure)
			in := tools.SearchInput{Query: "dentiste", Place: "75001"}

			// Each search also sends a geocoding request, which does not count.
			for i := range 2 {
				if _, err := e.tools.Search(t.Context(), in); err != nil {
					t.Fatalf("search %d: %v", i+1, err)
				}
			}
			_, err := e.tools.Search(t.Context(), in)
			assertErrorPrefix(t, err, "budget")
			if n := len(e.fake.doctolibRequests()); n != 2 {
				t.Errorf("sent %d Doctolib requests, want 2", n)
			}
			if n := len(e.fake.geoRequests()); n != 3 {
				t.Errorf("sent %d geocoding requests, want 3", n)
			}
		})
	}
}

package tools_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestEveryEndpointGetsTheSameBrowserHeaders(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, err := range callAll(t, e) {
		if err != nil {
			t.Fatal(err)
		}
	}

	reqs := e.fake.doctolibRequests()
	if len(reqs) != 3 {
		t.Fatalf("got %d Doctolib requests, want 3", len(reqs))
	}
	first := reqs[0].Header
	ua := first.Get("User-Agent")
	if !strings.HasPrefix(ua, "Mozilla/5.0 ") || !strings.Contains(ua, " Chrome/"+testChrome+".0.0.0 ") {
		t.Errorf("User-Agent %q", ua)
	}
	if got := first.Get("Referer"); got != e.base+"/" {
		t.Errorf("Referer %q, want the site root %q", got, e.base+"/")
	}
	if first.Get("Accept") != "application/json" || first.Get("Accept-Language") != "fr-FR,fr;q=0.9" {
		t.Errorf("Accept %q, Accept-Language %q", first.Get("Accept"), first.Get("Accept-Language"))
	}
	for _, r := range reqs {
		for _, h := range []string{"User-Agent", "Accept", "Accept-Language", "Referer"} {
			if r.Header.Get(h) != first.Get(h) {
				t.Errorf("%s %s: %s %q differs from %q", r.Method, r.Path, h, r.Header.Get(h), first.Get(h))
			}
		}
		checkPostHeaders(t, r, e.base)
	}
}

func checkPostHeaders(t *testing.T, r request, base string) {
	t.Helper()
	wantOrigin, wantType := "", ""
	if r.Method == http.MethodPost {
		wantOrigin, wantType = base, "application/json"
	}
	if got := r.Header.Get("Origin"); got != wantOrigin {
		t.Errorf("%s %s: Origin %q, want %q", r.Method, r.Path, got, wantOrigin)
	}
	if got := r.Header.Get("Content-Type"); got != wantType {
		t.Errorf("%s %s: Content-Type %q, want %q", r.Method, r.Path, got, wantType)
	}
}

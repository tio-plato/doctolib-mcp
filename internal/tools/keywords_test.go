package tools_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tio-plato/doctolib-mcp/internal/tools"
)

// The words are already folded: Doctolib folds case and accents itself, so sending them either way would do.
func TestKeywordsSendsTheWordsAndOrganisesTheAnswer(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	out, err := e.tools.Keywords(t.Context(), tools.KeywordsInput{Query: "detartrage"})
	if err != nil {
		t.Fatal(err)
	}

	reqs := e.fake.doctolibRequests()
	if len(reqs) != 1 {
		t.Fatalf("got %d Doctolib requests, want 1", len(reqs))
	}
	if reqs[0].Method != http.MethodPost || reqs[0].Path != keywordsPath || reqs[0].RawQuery != "" {
		t.Errorf("request is %s %s?%s", reqs[0].Method, reqs[0].Path, reqs[0].RawQuery)
	}
	assertJSON(t, json.RawMessage(reqs[0].Body), `{"query": "detartrage"}`)
	want := `{
		"keywords": [
			{"slug": "detartrage", "name": "Détartrage", "kind": "SPECIALITY",
			 "other": {"id": "9001", "testOnlyUnknownField": "kept"}},
			{"slug": "centre-dentaire", "name": "Centre dentaire", "kind": "ORGANIZATION_STATUS", "other": {"id": "9002"}}
		],
		"profiles": [
			{"profile_url": "<base>/dentiste/paris/ada-example",
			 "other": {"profileId": "4001", "type": "INDIVIDUAL_PRACTITIONER", "title": "Dr", "firstName": "Ada",
			           "name": "EXAMPLE", "speciality": "Chirurgien-dentiste", "city": "Paris",
			           "organizationStatus": null, "cloudinaryPublicId": "fake-picture-1", "portalClientId": null}},
			{"profile_url": "<base>/centre-dentaire/paris/centre-dentaire-exemple",
			 "other": {"profileId": "4002", "type": "ORGANIZATION", "title": null, "firstName": null,
			           "name": "Centre dentaire Exemple", "speciality": null, "city": "Paris",
			           "organizationStatus": "Centre dentaire", "cloudinaryPublicId": null, "portalClientId": null}}
		],
		"other": {"testOnlyTopLevel": {"kept": true}}
	}`
	assertJSON(t, out, strings.ReplaceAll(want, "<base>", e.base))
}

func TestKeywordsForWordsThatMatchNothing(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fake.handle(keywordsPath, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, `{"profiles": [], "searchEntries": []}`)
	})

	out, err := e.tools.Keywords(t.Context(), tools.KeywordsInput{Query: "detartrage"})
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, out, `{"keywords": [], "profiles": [], "other": {}}`)
}

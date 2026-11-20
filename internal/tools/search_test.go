package tools_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/tio-plato/doctolib-mcp/internal/tools"
)

func TestSearchSendsTheFullPlaceAndFilters(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	_, err := e.tools.Search(
		t.Context(),
		tools.SearchInput{Query: "dentiste", Place: "75001", Languages: []string{"en", "es"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.tools.Search(t.Context(), tools.SearchInput{Query: "dentiste", Place: "75001", Page: 2})
	if err != nil {
		t.Fatal(err)
	}

	reqs := e.fake.doctolibRequests()
	if len(reqs) != 2 {
		t.Fatalf("got %d Doctolib requests, want 2", len(reqs))
	}
	if reqs[0].Method != http.MethodPost || reqs[0].Path != searchPath || reqs[0].RawQuery != "page=0" {
		t.Errorf("first request is %s %s?%s", reqs[0].Method, reqs[0].Path, reqs[0].RawQuery)
	}
	// The box of geo_75001.json spans latitudes 48.854–48.87 and longitudes 2.32–2.351; its centre is (48.86, 2.3364).
	place := `{"name": "Paris 1er Arrondissement", "slug": "paris-1er-arrondissement", "country": "fr", "type": "locality",
		"viewport": {"northeast": {"lat": 48.87, "lng": 2.351}, "southwest": {"lat": 48.854, "lng": 2.32}},
		"gpsPoint": {"lat": 48.86, "lng": 2.3364}}`
	assertJSON(t, json.RawMessage(reqs[0].Body),
		`{"keyword": "dentiste", "location": {"place": `+place+`}, "filters": {"languages": ["gb", "es"]}}`)
	if reqs[1].RawQuery != "page=2" {
		t.Errorf("second request query is %q, want page=2", reqs[1].RawQuery)
	}
	assertJSON(
		t,
		json.RawMessage(reqs[1].Body),
		`{"keyword": "dentiste", "location": {"place": `+place+`}, "filters": {}}`,
	)
}

func TestSearchOrganisesResults(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	out, err := e.tools.Search(t.Context(), tools.SearchInput{Query: "dentiste", Place: "75001"})
	if err != nil {
		t.Fatal(err)
	}

	assertJSON(t, out.Place, `{"name": "Paris 1er Arrondissement", "insee": "75101", "postcodes": ["75001"],
		"bbox": {"south": 48.854, "west": 2.32, "north": 48.87, "east": 2.351}, "centre": {"lat": 48.86, "lng": 2.3364}}`)
	assertJSON(t, out.SearchedLocation, `{"lat": 48.83, "lng": 2.34}`)
	if out.Total == nil || *out.Total != 45 || out.Page != 0 || out.PageSize != 20 || !out.HasMore {
		t.Errorf("paging: total %v, page %d, page_size %d, has_more %v", out.Total, out.Page, out.PageSize, out.HasMore)
	}
	assertJSON(t, out.Other, `{"testOnlyTopLevel": {"kept": true}}`)
	assertJSON(t, out.FilterWarnings, `[]`)
	if len(out.Results) != 4 {
		t.Fatalf("got %d results, want 4", len(out.Results))
	}

	// The first result sits 0.009° of latitude north of searchedLocation: 1.0 km.
	assertJSON(t, out.Results[0], `{
		"name": "Dr Ada EXAMPLE", "kind": "practitioner", "title": "Dr", "first_name": " Ada", "last_name": "EXAMPLE ",
		"speciality": "Dentiste", "speciality_slug": "dentiste",
		"address": "1 Rue de l'Exemple", "zipcode": "75001", "city": "Paris", "lat": 48.839, "lng": 2.34,
		"distance_km": 1.0, "in_area": true,
		"languages": [{"code": "gb", "iso": "en", "name": "English", "label": "Anglais"},
			{"code": "fr", "iso": "fr", "name": "French", "label": "Français"},
			{"code": "zz", "iso": null, "name": null, "label": null}],
		"sector": {"code": "contracted", "label": "Conventionné"},
		"telehealth": true, "bookable_online": true, "allow_new_patients": true,
		"payment_means": ["cash", "credit_card"], "minimum_fee": null,
		"matched_motive": {"id": 3001, "name": "Première consultation"},
		"availability_query": {"visit_motive_id": 3001, "agenda_ids": [1001], "practice_id": 2001},
		"profile_url": "`+e.base+`/dentiste/paris/ada-example?pid=practice-2001",
		"other": {
			"cloudinaryPublicId": "fake-picture-1", "id": "profile-4001;practice-2001;dentiste",
			"location": {"country": "fr", "distanceInMeters": 1000.75},
			"references": {"id": 4001, "type": "public_profile"},
			"services": ["profile", "onlineBooking"], "administrativeArea": [], "exactMatch": true, "gender": "female",
			"matchedVisitMotive": {"fee": null, "insuranceSector": {"split": false, "type": null}},
			"onlineBooking": {"agendaIds": [1001, 1005], "bookableOrganizationSpecialities": []},
			"organizationStatus": null, "type": "INDIVIDUAL_PRACTITIONER", "testOnlyUnknownField": "kept"
		}}`)

	org := out.Results[1]
	if org.Kind != "organization" || org.Name != "Centre dentaire Exemple" || org.LastName != nil {
		t.Errorf("organisation: kind %q, name %q, last_name %v", org.Kind, org.Name, org.LastName)
	}
	if org.InArea {
		t.Error("a result in 75002 is in the 75001 area")
	}
	// Its matched motive has no agenda, so onlineBooking's are used.
	assertJSON(t, org.AvailabilityQuery, `{"visit_motive_id": 3002, "agenda_ids": [1002, 1003], "practice_id": 2002}`)
	assertJSON(t, org.Other["matchedVisitMotive"],
		`{"agendaIds": [], "fee": null, "insuranceSector": {"split": false, "type": null}}`)
	assertJSON(t, org.Other["onlineBooking"], `{"bookableOrganizationSpecialities": ["2 dentistes"]}`)

	notBookable := out.Results[2]
	if notBookable.BookableOnline || notBookable.AvailabilityQuery != nil || notBookable.DistanceKm != nil {
		t.Errorf("result without online booking nor position: bookable %v, query %v, distance %v",
			notBookable.BookableOnline, notBookable.AvailabilityQuery, notBookable.DistanceKm)
	}
	noPractice := out.Results[3]
	if noPractice.AvailabilityQuery != nil {
		t.Errorf("result without practiceId has availability_query %v", noPractice.AvailabilityQuery)
	}
	assertJSON(t, noPractice.Other["matchedVisitMotive"],
		`{"agendaIds": [1004], "fee": null, "insuranceSector": {"split": false, "type": null}}`)
	assertJSON(t, noPractice.Other["onlineBooking"], `{"agendaIds": [1004], "bookableOrganizationSpecialities": []}`)
}

func TestSearchDistanceFromBoxCentreWithoutSearchedLocation(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	var resp map[string]any
	if err := json.Unmarshal(readFile(t, "search.json"), &resp); err != nil {
		t.Fatal(err)
	}
	delete(resp, "searchedLocation")
	// The box centre is (48.862, 2.3355); 0.009° north of it is 1.0 km away, but 1.2 km from place.centre.
	first, ok := resp["healthcareProviders"].([]any)[0].(map[string]any)
	if !ok {
		t.Fatal("bad fixture")
	}
	location, ok := first["location"].(map[string]any)
	if !ok {
		t.Fatal("bad fixture")
	}
	location["lat"], location["lng"] = 48.871, 2.3355
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	e.fake.handle(searchPath, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusPartialContent, string(body))
	})

	out, err := e.tools.Search(t.Context(), tools.SearchInput{Query: "dentiste", Place: "75001"})
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, out.SearchedLocation, `{"lat": 48.862, "lng": 2.3355}`)
	if d := out.Results[0].DistanceKm; d == nil || *d != 1.0 {
		t.Errorf("distance_km is %v, want 1.0", d)
	}
}

func commune(name, code string, lat, lng float64, postcodes ...string) string {
	pc, _ := json.Marshal(postcodes)
	return fmt.Sprintf(
		`{"nom": %q, "code": %q, "codesPostaux": %s, "centre": {"type": "Point", "coordinates": [%g, %g]},
		"bbox": {"type": "Polygon", "coordinates": [[[%g, %g], [%g, %g], [%g, %g], [%g, %g], [%g, %g]]]}}`,
		name,
		code,
		pc,
		lng,
		lat,
		lng-0.01,
		lat-0.01,
		lng+0.01,
		lat-0.01,
		lng+0.01,
		lat+0.01,
		lng-0.01,
		lat+0.01,
		lng-0.01,
		lat-0.01,
	)
}

func TestSearchResolvesPlaces(t *testing.T) {
	t.Parallel()
	geo := map[string]string{
		"codePostal=01000&type=arrondissement-municipal": `[]`,
		"codePostal=01000": "[" + commune("Bourg-en-Bresse", "01053", 46.2, 5.22, "01000") + "," +
			commune("Saint-Denis-lès-Bourg", "01344", 46.2, 5.18, "01000") + "]",
		"nom=Paris": "[" + commune("Parisot", "81202", 43.8, 1.83, "81310") + "," +
			commune("Paris", "75056", 48.86, 2.35, "75001", "75002") + "]",
		"nom=saint etienne": "[" + commune("Saint-Étienne-du-Rouvray", "76575", 49.38, 1.1, "76800") + "," +
			commune("Saint-Étienne", "42218", 45.43, 4.39, "42000", "42100") + "]",
	}
	for _, tc := range []struct {
		input, name, insee string
	}{
		{"01000", "Bourg-en-Bresse", "01053"},
		{"Paris", "Paris", "75056"},
		{"saint etienne", "Saint-Étienne", "42218"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.fake.handle(geoPath, geoHandler(geo))

			out, err := e.tools.Search(t.Context(), tools.SearchInput{Query: "dentiste", Place: tc.input})
			if err != nil {
				t.Fatal(err)
			}
			if out.Place.Name != tc.name || out.Place.INSEE != tc.insee {
				t.Errorf("resolved to %s (%s), want %s (%s)", out.Place.Name, out.Place.INSEE, tc.name, tc.insee)
			}
			if q := e.fake.geoRequests()[0].RawQuery; !strings.Contains(q, "codePostal=") &&
				!strings.Contains(q, "boost=population") {
				t.Errorf("name lookup %q lacks boost=population", q)
			}
		})
	}
}

func TestSearchRejectsUnclearPlaces(t *testing.T) {
	t.Parallel()
	geo := map[string]string{
		"nom=Saint-Denis": "[" + commune("Saint-Denis", "97411", -20.9, 55.45, "97400", "97490") + "," +
			commune("Saint-Denis", "93066", 48.93, 2.36, "93200", "93210") + "," +
			commune("Saint-Denis-en-Val", "45274", 47.88, 1.96, "45560") + "]",
		"nom=Atlantis": "[" + commune("Arlanc", "63010", 45.41, 3.73, "63220") + "]",
	}
	for _, tc := range []struct {
		input string
		want  []string
	}{
		{"Saint-Denis", []string{"97400", "93200"}},
		{"Atlantis", []string{"Arlanc", "63220"}},
	} {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.fake.handle(geoPath, geoHandler(geo))

			_, err := e.tools.Search(t.Context(), tools.SearchInput{Query: "dentiste", Place: tc.input})
			text := assertErrorPrefix(t, err, "place")
			for _, w := range tc.want {
				if !strings.Contains(text, w) {
					t.Errorf("error %q does not name %s", text, w)
				}
			}
			if n := len(e.fake.doctolibRequests()); n != 0 {
				t.Errorf("sent %d Doctolib requests for a place error", n)
			}
		})
	}
}

func geoHandler(answers map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		key := "nom=" + q.Get("nom")
		if pc := q.Get("codePostal"); pc != "" {
			key = "codePostal=" + pc
			if typ := q.Get("type"); typ != "" {
				key += "&type=" + typ
			}
		}
		answer, ok := answers[key]
		if !ok {
			answer = "[]"
		}
		writeJSON(w, http.StatusOK, answer)
	}
}

func TestSearchKeepsEmptyObjects(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	var resp map[string]any
	if err := json.Unmarshal(readFile(t, "search.json"), &resp); err != nil {
		t.Fatal(err)
	}
	resp["searchedLocation"] = map[string]any{}
	first, ok := resp["healthcareProviders"].([]any)[0].(map[string]any)
	if !ok {
		t.Fatal("bad fixture")
	}
	first["speciality"] = map[string]any{}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	e.fake.handle(searchPath, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusPartialContent, string(body))
	})

	out, err := e.tools.Search(t.Context(), tools.SearchInput{Query: "dentiste", Place: "75001"})
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, out.Other["searchedLocation"], `{}`)
	assertJSON(t, out.Results[0].Other["speciality"], `{}`)
}

func TestSearchPassesTheVerifiedFilters(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	_, err := e.tools.Search(t.Context(), tools.SearchInput{
		Query: "dermatologue", Place: "75001", Telehealth: ptr(true),
		Sectors: []string{"contracted_2", "organization_contracted"}, AvailableWithinDays: ptr(7),
	})
	if err != nil {
		t.Fatal(err)
	}

	var body struct {
		Filters json.RawMessage `json:"filters"`
	}
	err = json.Unmarshal(e.fake.doctolibRequests()[0].Body, &body)
	if err != nil {
		t.Fatal(err)
	}
	// testNow is 17 November in Paris: 7 days ends at midnight on 24 November, Paris time, sent in UTC.
	assertJSON(t, body.Filters, `{"telehealth": true, "regulationSector": ["CONTRACTED_2", "ORGANIZATION_CONTRACTED"],
		"availabilitiesBefore": "2026-11-23T23:00:00.000Z"}`)
}

func TestSearchRefusesDoctolibOwnLanguageCodes(t *testing.T) {
	t.Parallel()
	// gb is Doctolib's code for English (ISO en); la is ISO for Latin but Doctolib's code for Lao (ISO lo);
	// dz is ISO for Dzongkha but Doctolib's code for Kabyle (ISO kab).
	for code, want := range map[string]string{"gb": `"en"`, "la": `"lo"`, "dz": `Kabyle (ISO "kab")`} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			_, err := e.tools.Search(
				t.Context(),
				tools.SearchInput{Query: "dentiste", Place: "75001", Languages: []string{code}},
			)
			text := assertErrorPrefix(t, err, "error")
			if !strings.Contains(text, want) {
				t.Errorf("error %q does not point to %s", text, want)
			}
			if n := len(e.fake.doctolibRequests()) + len(e.fake.geoRequests()); n != 0 {
				t.Errorf("sent %d requests", n)
			}
		})
	}
}

func TestSearchTakesThreeLetterISOCodes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	// Other ISO 639 codes of Wolof (wo), English (en) and Norwegian (no); en and eng name the same language.
	out, err := e.tools.Search(t.Context(), tools.SearchInput{
		Query: "dentiste", Place: "75001", Languages: []string{"wol", "eng", "nob", "en"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Filters struct {
			Languages []string `json:"languages"`
		} `json:"filters"`
	}
	err = json.Unmarshal(e.fake.doctolibRequests()[0].Body, &body)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, body.Filters.Languages, `["wol", "gb", "no"]`)
	for _, w := range out.FilterWarnings {
		if strings.Contains(w, "does not know") {
			t.Errorf("warned about a known code: %s", w)
		}
	}
}

func TestSearchWarnsAboutFiltersDoctolibIgnored(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	// In search.json, telehealth is true, false, null and false; languages are [gb fr zz], [fr], [fr] and [es];
	// sectors are contracted, organization_contracted, null and contracted.
	out, err := e.tools.Search(t.Context(), tools.SearchInput{
		Query: "dentiste", Place: "75001", Telehealth: ptr(true), Languages: []string{"fr", "qq"},
		Sectors: []string{"contracted", "organization_contracted"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var body struct {
		Filters struct {
			Languages []string `json:"languages"`
		} `json:"filters"`
	}
	err = json.Unmarshal(e.fake.doctolibRequests()[0].Body, &body)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, body.Filters.Languages, `["fr", "qq"]`)

	warnings := strings.Join(out.FilterWarnings, "\n")
	if len(out.FilterWarnings) != 3 {
		t.Fatalf("got %d warnings, want 3:\n%s", len(out.FilterWarnings), warnings)
	}
	for _, want := range []string{"does not know qq", "telehealth: 2 of 4", "1 could not be checked", "languages: 1 of 4"} {
		if !strings.Contains(warnings, want) {
			t.Errorf("warnings lack %q:\n%s", want, warnings)
		}
	}
	if strings.Contains(warnings, "sectors") {
		t.Errorf("warned about sectors, which every result that has one matches:\n%s", warnings)
	}
}

func TestSearchChecksLanguagesByDoctolibCode(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	// en is sent as gb: only the first of the four results in search.json declares it.
	out, err := e.tools.Search(
		t.Context(),
		tools.SearchInput{Query: "dentiste", Place: "75001", Languages: []string{"en"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.FilterWarnings) != 1 || !strings.Contains(out.FilterWarnings[0], "languages: 3 of 4") {
		t.Errorf("warnings %q, want one about 3 of 4 results", out.FilterWarnings)
	}
}

func TestSearchWarnsWhenNobodySpeaksTheLanguage(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		page  int
		total int
		warn  bool
	}{
		"nobody on the first page":   {page: 0, total: 0, warn: true},
		"nobody, asked for page 3":   {page: 3, total: 0, warn: true},
		"page 3 past the last match": {page: 3, total: 25, warn: false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.fake.handle(searchPath, func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(
					w,
					http.StatusPartialContent,
					fmt.Sprintf(`{"total": %d, "healthcareProviders": []}`, tc.total),
				)
			})

			out, err := e.tools.Search(t.Context(), tools.SearchInput{
				Query: "dentiste", Place: "75001", Languages: []string{"es"}, Page: tc.page,
			})
			if err != nil {
				t.Fatal(err)
			}
			warned := slices.ContainsFunc(
				out.FilterWarnings,
				func(w string) bool { return strings.Contains(w, "no result") },
			)
			if warned != tc.warn {
				t.Errorf("warnings %q, want a no-result warning: %v", out.FilterWarnings, tc.warn)
			}
		})
	}
}

func TestSearchSendsUnknownLanguageCodesOnce(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	out, err := e.tools.Search(t.Context(), tools.SearchInput{
		Query: "dentiste", Place: "75001", Languages: []string{"qq", "qq"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Filters struct {
			Languages []string `json:"languages"`
		} `json:"filters"`
	}
	err = json.Unmarshal(e.fake.doctolibRequests()[0].Body, &body)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, body.Filters.Languages, `["qq"]`)
	if w := strings.Join(out.FilterWarnings, "\n"); strings.Count(w, "qq") != 1 {
		t.Errorf("warnings name qq more than once:\n%s", w)
	}
}

func TestSearchLabelsUnknownSectorWithNull(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	body := strings.Replace(string(readFile(t, "search.json")), `"regulationSector": "contracted",`,
		`"regulationSector": "testOnlyUnknownSector",`, 1)
	e.fake.handle(searchPath, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusPartialContent, body)
	})

	out, err := e.tools.Search(t.Context(), tools.SearchInput{Query: "dentiste", Place: "75001"})
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, out.Results[0].Sector, `{"code": "testOnlyUnknownSector", "label": null}`)
}

func TestSearchFieldOfAnotherTypeIsNull(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	body := string(readFile(t, "search.json"))
	body = strings.Replace(body, `"visitMotiveId": 3001`, `"visitMotiveId": "3001"`, 1)
	body = strings.Replace(body, `"lat": 48.839`, `"lat": "48.839"`, 1)
	e.fake.handle(searchPath, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusPartialContent, body)
	})

	out, err := e.tools.Search(t.Context(), tools.SearchInput{Query: "dentiste", Place: "75001"})
	if err != nil {
		t.Fatal(err)
	}
	got := asJSON(t, out.Results[0])
	// Null: not an availability_query for motive 0 or a distance from latitude 0.
	for _, key := range []string{"availability_query", "lat", "distance_km"} {
		if got[key] != nil {
			t.Errorf("%s is %v, want null", key, got[key])
		}
	}
	assertJSON(t, got["matched_motive"], `{"id": null, "name": "Première consultation"}`)
}

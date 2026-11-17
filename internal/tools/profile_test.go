package tools_test

import (
	"maps"
	"net/http"
	"slices"
	"testing"

	"github.com/tio-plato/doctolib-mcp/internal/tools"
)

func TestProfileOrganisesMotivesAndAgendas(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	profileURL := "https://www.doctolib.fr/dentiste/paris/ada-example?pid=practice-2001"
	out, err := e.tools.Profile(t.Context(), tools.ProfileInput{ProfileURL: profileURL})
	if err != nil {
		t.Fatal(err)
	}

	reqs := e.fake.doctolibRequests()
	if len(reqs) != 1 || reqs[0].Method != http.MethodGet || reqs[0].Path != profilePath ||
		reqs[0].RawQuery != "profile_slug=ada-example&locale=fr" {
		t.Fatalf("requests: %+v", reqs)
	}
	if out.Name == nil || *out.Name != "Dr Ada EXAMPLE" || out.Speciality == nil || *out.Speciality != "Dentiste" ||
		out.TelehealthEnabled == nil || !*out.TelehealthEnabled || out.ProfileURL != profileURL {
		t.Errorf("header: name %v, speciality %v, telehealth %v, url %q",
			out.Name, out.Speciality, out.TelehealthEnabled, out.ProfileURL)
	}
	assertJSON(t, out.Places, `[
		{"practice_id": 2001, "name": "Cabinet dentaire Exemple", "address": "1 Rue de l'Exemple", "zipcode": "75001",
		 "city": "Paris", "lat": 48.861, "lng": 2.336, "phone": "01 00 00 00 00",
		 "other": {"id": "practice-2001", "slug": "paris"}},
		{"practice_id": 2002, "name": "Clinique Exemple", "address": "2 Rue de l'Exemple", "zipcode": "75002",
		 "city": "Paris", "lat": 48.866, "lng": 2.343, "phone": null,
		 "other": {"id": "practice-2002", "slug": "paris"}}]`)

	// Agenda 1003 is disabled: it is in unavailable_agendas, not in offered.
	restriction := `{"insurance_sector": "all", "agenda_ids": [1002]}`
	assertJSON(t, out.Motives[0], `{
		"id": 3001, "name": "Première consultation", "telehealth": false, "category": null,
		"new_patient_restrictions": [`+restriction+`],
		"offered": [
			{"practice_id": 2001, "practitioner": "Dr Ada EXAMPLE", "agenda_id": 1001, "new_patient_restrictions": [],
			 "availability_query": {"visit_motive_id": 3001, "agenda_ids": [1001], "practice_id": 2001}},
			{"practice_id": 2001, "practitioner": "Dr Bob EXAMPLE", "agenda_id": 1002,
			 "new_patient_restrictions": [`+restriction+`],
			 "availability_query": {"visit_motive_id": 3001, "agenda_ids": [1002], "practice_id": 2001}}],
		"other": {"visit_motive_category_id": null, "organization_id": 9001, "position": 1,
			"age_referral_restrictions": [], "configurations": null, "testOnlyUnknownField": "kept"}}`)
	if out.Motives[1].Category == nil || *out.Motives[1].Category != "Suivi" || len(out.Motives[1].Offered) != 1 {
		t.Errorf("second motive: category %v, offered %+v", out.Motives[1].Category, out.Motives[1].Offered)
	}
	assertJSON(
		t,
		out.UnavailableAgendas,
		`[{"agenda_id": 1003, "practice_id": 2002, "practitioner": "Dr Ada EXAMPLE"}]`,
	)
	assertJSON(t, out.Practitioners[0], `{"id": 5001, "name": "Dr Ada EXAMPLE", "other": {"profile_id": 4001,
		"name": "EXAMPLE ", "name_with_title_and_determiner": "Dr Ada Example", "speciality": "Dentiste"}}`)

	// What is left of data, at the top of other, once and only once: not the organised
	// places, practitioners and motives, and not again under "data".
	otherKeys := slices.Sorted(maps.Keys(out.Other))
	wantKeys := []string{
		"agendas",
		"filtered_visit_motives",
		"profile",
		"specialities",
		"testOnlyUnknownField",
		"visit_motive_categories",
	}
	if !slices.Equal(otherKeys, wantKeys) {
		t.Errorf("other has %v, want %v", otherKeys, wantKeys)
	}
	assertJSON(t, out.Other["profile"], `{"id": 4001, "name_with_title_and_determiner": "Dr Ada Example",
		"subtitle": "Dentiste", "speciality": {"id": 7, "slug": "dentiste", "kind": "medical"}, "organization": false}`)
}

func TestProfileKeepsBothFieldsOfTheSameName(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fake.handle(profilePath, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, `{"meta": "outer value", "data": {"meta": "inner value", "visit_motives": []}}`)
	})

	out, err := e.tools.Profile(t.Context(), tools.ProfileInput{ProfileURL: e.base + "/dentiste/paris/ada-example"})
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, out.Other, `{"meta": "outer value", "data": {"meta": "inner value"}}`)
}

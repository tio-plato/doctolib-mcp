package tools_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tio-plato/doctolib-mcp/internal/gate"
	"github.com/tio-plato/doctolib-mcp/internal/tools"
)

// The fixture's agendas and motives as organised, for the tests that pick motives.
const (
	agenda1001 = `{"agenda_id": 1001, "practice_id": 2001, "practitioner_id": 5001, "practitioner": "Dr Ada EXAMPLE",
		"booking_temporary_disabled": false, "other": {"anonymous": false, "landline_number": "01 00 00 00 00"}}`
	motive3002 = `{"id": 3002, "name": "Consultation de suivi", "telehealth": true, "category": "Suivi",
		"new_patient_restrictions": [{"insurance_sector": "all", "agenda_ids": [1001, 1099]}],
		"age_referral_restrictions": [
			{"age": {"age": 15, "operator": "lt", "unit": "years"}, "agenda_ids": [1001], "insurance_sector": "all"},
			{"insurance_sector": "all"}],
		"offered": [{"visit_motive_id": 3002, "agenda_ids": [1001], "practice_id": 2001},
			{"visit_motive_id": 3002, "agenda_ids": [1001], "practice_id": 2002}],
		"other": {"position": 2, "visit_motive_category_id": 50}}`
	practitioner5001 = `{"id": 5001, "name": "Dr Ada EXAMPLE", "other": {"profile_id": 4001, "speciality": "Dentiste"}}`
)

// otherKeys is what is left of the fixture's data, at the top of other, once and only once: not the organised
// places, practitioners, agendas and motives, and not again under "data".
func otherKeys() []string {
	return []string{
		"filtered_visit_motives",
		"profile",
		"specialities",
		"testOnlyUnknownField",
		"visit_motive_categories",
	}
}

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
	// floor is null: it leaves other.
	assertJSON(t, out.Places, `[
		{"practice_id": 2001, "name": "Cabinet dentaire Exemple", "address": "1 Rue de l'Exemple", "zipcode": "75001",
		 "city": "Paris", "lat": 48.861, "lng": 2.336, "phone": "01 00 00 00 00",
		 "other": {"id": "practice-2001", "slug": "paris"}},
		{"practice_id": 2002, "name": "Clinique Exemple", "address": "2 Rue de l'Exemple", "zipcode": "75002",
		 "city": "Paris", "lat": 48.866, "lng": 2.343, "phone": null,
		 "other": {"id": "practice-2002", "slug": "paris"}}]`)

	got := asJSON(t, out)
	// The motive lists leave other only when offered says all they say: 1002 lists a motive the profile doesn't
	// have, 1003 has booking disabled, and 1004's visit_motive_ids names another motive than its map.
	assertJSON(t, got["agendas"], `[`+agenda1001+`,
		{"agenda_id": 1002, "practice_id": 2001, "practitioner_id": 5002, "practitioner": "Dr Bob EXAMPLE",
		 "booking_temporary_disabled": false,
		 "other": {"anonymous": false, "visit_motive_ids": [3001, 3999],
			"visit_motive_ids_by_practice_id": {"2001": [3001, 3999]}}},
		{"agenda_id": 1003, "practice_id": 2002, "practitioner_id": 5001, "practitioner": "Dr Ada EXAMPLE",
		 "booking_temporary_disabled": true,
		 "other": {"anonymous": false, "visit_motive_ids": [3001], "visit_motive_ids_by_practice_id": {"2002": [3001]}}},
		{"agenda_id": 1004, "practice_id": 2002, "practitioner_id": null, "practitioner": null,
		 "booking_temporary_disabled": false, "other": {"anonymous": true, "visit_motive_ids": [3002]}}]`)

	// Agenda 1003 is disabled: it is in no offered, but its restrictions stay, as does 1099, which no agenda has.
	assertJSON(t, out.Motives[0], `{
		"id": 3001, "name": "Première consultation", "telehealth": false, "category": null,
		"new_patient_restrictions": [{"insurance_sector": "all", "agenda_ids": [1002]}],
		"age_referral_restrictions": [
			{"age": {"age": 13, "operator": "lt", "unit": "years"}, "agenda_ids": [1001, 1003, 1099],
			 "insurance_sector": "all"}],
		"offered": [{"visit_motive_id": 3001, "agenda_ids": [1001, 1002], "practice_id": 2001},
			{"visit_motive_id": 3001, "agenda_ids": [1004], "practice_id": 2002}],
		"other": {"position": 1, "testOnlyUnknownField": "kept"}}`)
	// Agenda 1001 offers 3002 at practice 2002 too, though its own practice is 2001. 1002 and 1004 don't offer
	// 3002: they leave the restrictions, and the entry that names only them goes.
	assertJSON(t, out.Motives[1], motive3002)
	assertJSON(t, out.Practitioners, `[`+practitioner5001+`,
		{"id": 5002, "name": "Dr Bob EXAMPLE", "other": {"profile_id": 4005, "speciality": "Dentiste"}}]`)

	if keys := slices.Sorted(maps.Keys(out.Other)); !slices.Equal(keys, otherKeys()) {
		t.Errorf("other has %v, want %v", keys, otherKeys())
	}
	assertJSON(t, out.Other["profile"], `{"id": 4001, "subtitle": "Dentiste",
		"speciality": {"id": 7, "slug": "dentiste", "kind": "medical"}, "organization": false}`)
	for _, key := range []string{"motive_index", "remaining_motive_ids"} {
		if _, ok := got[key]; ok {
			t.Errorf("a profile that fits has %s", key)
		}
	}
}

func TestProfileMotiveIDs(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	cs := connect(t, e)
	profileURL := e.base + "/dentiste/paris/ada-example"

	// 4242 is no motive of this profile.
	got := callTool(t, cs, "practitioner_profile", map[string]any{
		"profile_url": profileURL, "motive_ids": []int{3002, 4242},
	})
	assertJSON(t, got["motives"], `[`+motive3002+`]`)
	assertJSON(t, got["agendas"], `[`+agenda1001+`]`)
	assertJSON(t, got["practitioners"], `[`+practitioner5001+`]`)
	assertJSON(t, pluck(t, got["places"], "practice_id"), `[2001, 2002]`)
	other, _ := got["other"].(map[string]any)
	if keys := slices.Sorted(maps.Keys(other)); !slices.Equal(keys, otherKeys()) {
		t.Errorf("other has %v, want %v", keys, otherKeys())
	}
	for _, key := range []string{"motive_index", "remaining_motive_ids"} {
		if _, ok := got[key]; ok {
			t.Errorf("motive_ids that fit got %s", key)
		}
	}

	// The disabled agenda 1003 lists 3001 too; practitioner 5001 has two of these agendas and comes once.
	got = callTool(t, cs, "practitioner_profile", map[string]any{"profile_url": profileURL, "motive_ids": []int{3001}})
	assertJSON(t, pluck(t, got["agendas"], "agenda_id"), `[1001, 1002, 1003, 1004]`)
	assertJSON(t, pluck(t, got["practitioners"], "id"), `[5001, 5002]`)
}

func TestProfileOverBudget(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fake.handle(profilePath, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, bigProfile())
	})
	cs := connect(t, e)
	profileURL := e.base + "/centre-dentaire/paris/centre-example"

	got := callTool(t, cs, "practitioner_profile", map[string]any{"profile_url": profileURL})
	for _, key := range []string{"motives", "agendas", "practitioners"} {
		assertJSON(t, got[key], `[]`)
	}
	assertJSON(t, got["other"], `{}`)
	assertJSON(t, got["motive_index"], `[
		{"id": 1001, "name": "Consultation 1001", "category": null, "telehealth": false},
		{"id": 1002, "name": "Consultation 1002", "category": null, "telehealth": false},
		{"id": 1003, "name": "Consultation 1003", "category": null, "telehealth": false}]`)
	if _, ok := got["remaining_motive_ids"]; ok {
		t.Error("the motive index has remaining_motive_ids")
	}

	// A motive with its 200 agendas fits; two don't, so the first stops the others.
	got = callTool(t, cs, "practitioner_profile", map[string]any{
		"profile_url": profileURL, "motive_ids": []int{1002, 1001, 1003},
	})
	agendas := sequence(201, 400)
	assertJSON(t, pluck(t, got["motives"], "offered"),
		`[[{"visit_motive_id": 1002, "agenda_ids": `+agendas+`, "practice_id": 2001}]]`)
	assertJSON(t, pluck(t, got["agendas"], "agenda_id"), agendas)
	assertJSON(t, got["remaining_motive_ids"], `[1001, 1003]`)
	assertJSON(t, pluck(t, got["places"], "practice_id"), `[2001]`)
	assertJSON(t, got["other"], `{"specialities": [{"id": 7, "name": "Dentiste"}]}`)
	if _, ok := got["motive_index"]; ok {
		t.Error("picked motives have motive_index")
	}

	// 1003 with its 300 agendas doesn't fit alone, but the first motive asked for always goes in.
	got = callTool(t, cs, "practitioner_profile", map[string]any{"profile_url": profileURL, "motive_ids": []int{1003}})
	assertJSON(t, pluck(t, got["motives"], "id"), `[1003]`)
	assertJSON(t, pluck(t, got["agendas"], "agenda_id"), sequence(401, 700))
	if _, ok := got["remaining_motive_ids"]; ok {
		t.Error("a single motive left remaining_motive_ids")
	}
}

// The motives left out are listed in the result, so they count against its size too.
func TestProfilePickedMotivesFitWithTheListOfTheRest(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fake.handle(profilePath, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, manyMotives())
	})
	cs := connect(t, e)

	asked := make([]int64, 0, 400)
	for id := int64(1001); id <= 1400; id++ {
		asked = append(asked, id)
	}
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "practitioner_profile", Arguments: map[string]any{
		"profile_url": e.base + "/centre-dentaire/paris/centre-example", "motive_ids": asked,
	}})
	if err != nil || res.IsError {
		t.Fatalf("%v %+v", err, res)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if n := utf8.RuneCountInString(text); n > 36000 {
		t.Errorf("result has %d characters, over 36,000", n)
	}
	var got struct {
		Motives []struct {
			ID int64 `json:"id"`
		} `json:"motives"`
		Remaining []int64 `json:"remaining_motive_ids"`
	}
	err = json.Unmarshal([]byte(text), &got)
	if err != nil {
		t.Fatal(err)
	}
	given := make([]int64, 0, len(asked))
	for _, m := range got.Motives {
		given = append(given, m.ID)
	}
	if len(got.Remaining) == 0 || !slices.Equal(append(given, got.Remaining...), asked) {
		t.Errorf("gave %v and left %v, want %v in two parts", given, got.Remaining, asked)
	}
}

// Without a motive to pick, the index is empty, but it still says the agendas were left out.
func TestProfileIndexOfNoMotive(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fake.handle(profilePath, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, centre(bigAgendas(), nil))
	})

	got := callTool(t, connect(t, e), "practitioner_profile", map[string]any{
		"profile_url": e.base + "/centre-dentaire/paris/centre-example",
	})
	assertJSON(t, got["agendas"], `[]`)
	assertJSON(t, got["motive_index"], `[]`)
}

func TestProfileKeepsTheProfileTenMinutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	in := tools.ProfileInput{ProfileURL: e.base + "/dentiste/paris/ada-example"}

	for i, step := range []struct {
		wait     time.Duration
		requests int
	}{
		{0, 1},
		{9*time.Minute + 59*time.Second, 1},
		// Ten minutes after the request: the kept profile is too old.
		{time.Second, 2},
	} {
		e.now = e.now.Add(step.wait)
		_, err := e.tools.Profile(t.Context(), in)
		if err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
		if n := len(e.fake.doctolibRequests()); n != step.requests {
			t.Errorf("call %d: %d Doctolib requests in all, want %d", i+1, n, step.requests)
		}
	}

	// The kept profile is fresh, but every Doctolib tool stays off while the marker is there.
	_, err := e.gate.Block(profilePath, http.StatusForbidden, "Votre requête est bloquée !")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.tools.Profile(t.Context(), in)
	var blocked *gate.BlockedError
	if !errors.As(err, &blocked) {
		t.Errorf("got %v, want a block error", err)
	}
	if n := len(e.fake.doctolibRequests()); n != 2 {
		t.Errorf("%d Doctolib requests in all, want 2", n)
	}
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

func TestProfileKeepsAnEmptyDataWrapper(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fake.handle(profilePath, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, `{"data": {}, "meta": 1, "places": [{"name": "Cabinet"}]}`)
	})

	out, err := e.tools.Profile(t.Context(), tools.ProfileInput{ProfileURL: e.base + "/dentiste/paris/ada-example"})
	if err != nil {
		t.Fatal(err)
	}
	// Fields next to the wrapper are not the profile's, empty wrapper or not.
	assertJSON(t, out.Other, `{"data": {}, "meta": 1, "places": [{"name": "Cabinet"}]}`)
	if len(out.Places) != 0 {
		t.Errorf("organised places from outside the data wrapper: %+v", out.Places)
	}
}

// bigProfile is a centre with 3 motives: 1001 and 1002 with 200 agendas each come to about 26,000 characters, and
// 1003 with 300 to about 39,000.
func bigProfile() string {
	motives := make([]string, 0, 3)
	for id := 1001; id <= 1003; id++ {
		motives = append(motives, fmt.Sprintf(`{"id": %d, "name": "Consultation %d", "telehealth": false, `+
			`"visit_motive_category_id": null}`, id, id))
	}
	return centre(bigAgendas(), motives)
}

// bigAgendas are 700 bookable agendas without a practitioner: 1–200 offer motive 1001, 201–400 1002, 401–700 1003.
func bigAgendas() []string {
	agendas := make([]string, 0, 700)
	for id := 1; id <= 700; id++ {
		agendas = append(agendas, fmt.Sprintf(`{"id": %d, "booking_temporary_disabled": false, "practice_id": 2001, `+
			`"practitioner_id": null, "visit_motive_ids_by_practice_id": {"2001": [%d]}}`, id, 1001+min(id-1, 400)/200))
	}
	return agendas
}

// centre is a profile at one practice, 2001.
func centre(agendas, motives []string) string {
	return `{"data": {
		"places": [{"id": "practice-2001", "name": "Centre dentaire Exemple", "practice_ids": [2001]}],
		"practitioners": [], "specialities": [{"id": 7, "name": "Dentiste"}],
		"agendas": [` + strings.Join(agendas, ", ") + `],
		"visit_motives": [` + strings.Join(motives, ", ") + `]}}`
}

// manyMotives is a centre with 400 motives, each offered by one agenda: about 340 characters a motive, so a result
// holds about a hundred of them.
func manyMotives() string {
	agendas := make([]string, 0, 400)
	motives := make([]string, 0, 400)
	for id := 1; id <= 400; id++ {
		agendas = append(agendas, fmt.Sprintf(`{"id": %d, "booking_temporary_disabled": false, "practice_id": 2001, `+
			`"practitioner_id": null, "visit_motive_ids_by_practice_id": {"2001": [%d]}}`, id, 1000+id))
		motives = append(
			motives,
			fmt.Sprintf(`{"id": %d, "name": "Consultation %d", "telehealth": false}`, 1000+id, 1000+id),
		)
	}
	return centre(agendas, motives)
}

// sequence is the JSON array of the integers from first to last.
func sequence(first, last int) string {
	ints := make([]string, 0, last-first+1)
	for i := first; i <= last; i++ {
		ints = append(ints, strconv.Itoa(i))
	}
	return "[" + strings.Join(ints, ", ") + "]"
}

// asJSON reads a result as a client does, fields the Go type doesn't have included.
func asJSON(t *testing.T, v any) map[string]any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	err = json.Unmarshal(data, &m)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// pluck lists one field of each object of a JSON array.
func pluck(t *testing.T, array any, key string) []any {
	t.Helper()
	items, ok := array.([]any)
	if !ok {
		t.Fatalf("%v is not an array", array)
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		obj, _ := item.(map[string]any)
		out = append(out, obj[key])
	}
	return out
}

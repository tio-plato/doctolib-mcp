package tools_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tio-plato/doctolib-mcp/internal/tools"
)

func availabilityByMotive(answers map[string]func(http.ResponseWriter)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		answer, ok := answers[r.URL.Query().Get("visit_motive_ids")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		answer(w)
	}
}

func jsonAnswer(body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) { writeJSON(w, http.StatusOK, body) }
}

func query(motive int64, agendas ...int64) tools.AvailabilityQuery {
	return tools.AvailabilityQuery{VisitMotiveID: motive, AgendaIDs: agendas, PracticeID: 2001}
}

func TestAvailabilitySendsOneRequestPerQuery(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	out, err := e.tools.Availability(t.Context(), tools.AvailabilityInput{
		Queries: []tools.AvailabilityQuery{query(3001, 1001, 1002), query(3002, 1003), query(3003, 1004)},
	})
	if err != nil {
		t.Fatal(err)
	}

	// testNow is still 16 November in UTC, already 17 November in Paris.
	want := []string{
		"start_date=2026-11-17&visit_motive_ids=3001&agenda_ids=1001-1002&practice_ids=2001&limit=15",
		"start_date=2026-11-17&visit_motive_ids=3002&agenda_ids=1003&practice_ids=2001&limit=15",
		"start_date=2026-11-17&visit_motive_ids=3003&agenda_ids=1004&practice_ids=2001&limit=15",
	}
	reqs := e.fake.doctolibRequests()
	if len(reqs) != len(want) {
		t.Fatalf("got %d requests, want %d", len(reqs), len(want))
	}
	for i, r := range reqs {
		if r.Method != http.MethodGet || r.Path != availabilityPath || r.RawQuery != want[i] {
			t.Errorf(
				"request %d is %s %s?%s, want GET %s?%s",
				i,
				r.Method,
				r.Path,
				r.RawQuery,
				availabilityPath,
				want[i],
			)
		}
	}
	assertJSON(t, out.Window, `{"start": "2026-11-17", "end": "2026-12-01"}`)
}

func TestAvailabilityStatuses(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fake.handle(availabilityPath, availabilityByMotive(map[string]func(http.ResponseWriter){
		"1": jsonAnswer(`{"total": 3, "availabilities": [
			{"date": "2026-11-17", "slots": ["2026-11-17T14:00:00.000+01:00", "2026-11-17T09:50:00.000+01:00"]},
			{"date": "2026-11-18", "slots": []},
			{"date": "2026-11-19", "slots": [{"start_date": "2026-11-19T08:30:00.000+01:00", "steps": []}]}],
			"next_slot": "2026-11-17T09:50:00.000+01:00"}`),
		"2": jsonAnswer(`{"total": 0, "availabilities": [{"date": "2026-11-17", "slots": []}],
			"reason": "not_opened_availability", "next_slot": "2027-01-12T10:00:00.000+01:00"}`),
		"3": jsonAnswer(`{"total": 0, "availabilities": [{"date": "2026-11-17", "slots": []}],
			"next_slot": "2027-01-26T09:50:00.000+01:00"}`),
		"4": jsonAnswer(`{"total": 0, "availabilities": [{"date": "2026-11-17", "slots": []}]}`),
		"5": jsonAnswer(`{"total": 0, "availabilities": [], "reason": "some_new_reason"}`),
	}))

	out, err := e.tools.Availability(t.Context(), tools.AvailabilityInput{
		Queries:   []tools.AvailabilityQuery{query(1, 1), query(2, 1), query(3, 1), query(4, 1), query(5, 1)},
		StartDate: "2026-11-17",
	})
	if err != nil {
		t.Fatal(err)
	}

	assertJSON(t, out.Results[0], `{"query": {"visit_motive_id": 1, "agenda_ids": [1], "practice_id": 2001},
		"status": "has_slots",
		"earliest": {"date": "2026-11-17", "time": "09:50", "iso": "2026-11-17T09:50:00.000+01:00", "source": "window"},
		"slot_count": 3,
		"slots": "Tue 2026-11-17 09:50 14:00; Thu 2026-11-19 08:30",
		"reason": null,
		"other": {"total": 3, "next_slot": "2026-11-17T09:50:00.000+01:00"}}`)
	assertJSON(t, out.Results[1], `{"query": {"visit_motive_id": 2, "agenda_ids": [1], "practice_id": 2001},
		"status": "not_opened",
		"earliest": {"date": "2027-01-12", "time": "10:00", "iso": "2027-01-12T10:00:00.000+01:00", "source": "next_slot"},
		"slot_count": 0, "slots": "", "reason": "not_opened_availability",
		"other": {"total": 0}}`)
	assertJSON(t, out.Results[2], `{"query": {"visit_motive_id": 3, "agenda_ids": [1], "practice_id": 2001},
		"status": "next_slot_known",
		"earliest": {"date": "2027-01-26", "time": "09:50", "iso": "2027-01-26T09:50:00.000+01:00", "source": "next_slot"},
		"slot_count": 0, "slots": "", "reason": null,
		"other": {"total": 0}}`)
	assertJSON(t, out.Results[3], `{"query": {"visit_motive_id": 4, "agenda_ids": [1], "practice_id": 2001},
		"status": "none_visible", "earliest": null, "slot_count": 0, "slots": "", "reason": null,
		"other": {"total": 0}}`)
	if r := out.Results[4]; r.Status != tools.StatusNoneVisible || r.Reason == nil || *r.Reason != "some_new_reason" {
		t.Errorf("unknown reason: status %q, reason %v", r.Status, r.Reason)
	}
}

func TestAvailabilitySlotsFormat(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fake.handle(availabilityPath, availabilityByMotive(map[string]func(http.ResponseWriter){
		"1": jsonAnswer(`{"total": 14, "availabilities": [
			{"date": "2026-11-17", "slots": [
				"2026-11-17T09:20:00.000+01:00", "2026-11-17T09:00:00.000+01:00", "2026-11-17T09:10:00.000+01:00",
				"2026-11-17T09:30:00.000+01:00", "2026-11-17T09:30:00.000+01:00", "2026-11-17T10:00:00.000+01:00",
				"2026-11-17T14:00:00.000+01:00", "2026-11-17T14:20:00.000+01:00", "2026-11-17T14:40:00.000+01:00",
				"2026-11-17T16:05:00.000+01:00"]},
			{"date": "2026-11-18", "slots": [
				"2026-11-18T00:30:00.000+01:00", "2026-11-18T08:15:00.000+01:00", "2026-11-18T09:15:00.000+01:00"]},
			{"date": "2026-11-19", "slots": ["2026-11-19T23:30:00.000-04:00"]}]}`),
	}))

	out, err := e.tools.Availability(t.Context(), tools.AvailabilityInput{
		Queries:   []tools.AvailabilityQuery{query(1, 1)},
		StartDate: "2026-11-17",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 00:30 in Paris is still 17 November in UTC, and 23:30 at -04:00 is already 20 November in UTC and in Paris:
	// the day and weekday come from the time as Doctolib writes it.
	r := out.Results[0]
	assertJSON(t, r.SlotCount, `14`)
	assertJSON(
		t,
		r.Slots,
		`"Tue 2026-11-17 09:00-09:30/10 10:00 14:00-14:40/20 16:05; Wed 2026-11-18 00:30 08:15 09:15; Thu 2026-11-19 23:30"`,
	)
}

func TestAvailabilitySubstitution(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fake.handle(availabilityPath, availabilityByMotive(map[string]func(http.ResponseWriter){
		"1": jsonAnswer(`{"total": 3, "availabilities": [
			{"date": "2026-11-17", "slots": [
				"2026-11-17T10:00:00.000+01:00", "2026-11-17T10:10:00.000+01:00", "2026-11-17T10:20:00.000+01:00"],
			 "substitution": {
				"Dr. Bo EXAMPLE": {"slots": ["2026-11-17T10:20:00.000+01:00"], "status": "collaborator"},
				"Dr. Ada EXAMPLE": {"slots": ["2026-11-17T10:00:00.000+01:00", "2026-11-17T10:10:00.000+01:00",
					"2026-11-18T08:00:00.000+01:00"], "status": "substitute"}}},
			{"date": "2026-11-18", "slots": [],
			 "substitution": {"Dr. Cy EXAMPLE": {"slots": "2026-11-18T09:00:00.000+01:00", "status": "substitute"}}}]}`),
	}))

	out, err := e.tools.Availability(t.Context(), tools.AvailabilityInput{
		Queries:   []tools.AvailabilityQuery{query(1, 1)},
		StartDate: "2026-11-17",
	})
	if err != nil {
		t.Fatal(err)
	}

	// A time goes under its own date, whatever day lists it. A substitution in another shape stays in other as
	// Doctolib gives it.
	assertJSON(t, out.Results[0], `{"query": {"visit_motive_id": 1, "agenda_ids": [1], "practice_id": 2001},
		"status": "has_slots",
		"earliest": {"date": "2026-11-17", "time": "10:00", "iso": "2026-11-17T10:00:00.000+01:00", "source": "window"},
		"slot_count": 3, "slots": "Tue 2026-11-17 10:00-10:20/10",
		"substitution": [
			{"day": "Tue 2026-11-17", "practitioner": "Dr. Ada EXAMPLE", "status": "substitute", "times": "10:00 10:10"},
			{"day": "Tue 2026-11-17", "practitioner": "Dr. Bo EXAMPLE", "status": "collaborator", "times": "10:20"},
			{"day": "Wed 2026-11-18", "practitioner": "Dr. Ada EXAMPLE", "status": "substitute", "times": "08:00"}],
		"reason": null,
		"other": {"total": 3, "availabilities": [{"date": "2026-11-18",
			"substitution": {"Dr. Cy EXAMPLE": {"slots": "2026-11-18T09:00:00.000+01:00", "status": "substitute"}}}]}}`)
}

func TestAvailabilitySubstitutionInAnotherShapeStaysWhole(t *testing.T) {
	t.Parallel()
	// Each breaks one rule of the shape seen on Doctolib: an extra field, a time that can't be read, no status,
	// and one entry right next to one whose slots aren't an array.
	shapes := []string{
		`{"Dr. Cy EXAMPLE": {"slots": ["2026-11-17T09:00:00.000+01:00"], "status": "substitute", "agenda_id": 5}}`,
		`{"Dr. Cy EXAMPLE": {"slots": ["2026-11-17T09:00:00.000+01:00", "09:20"], "status": "substitute"}}`,
		`{"Dr. Cy EXAMPLE": {"slots": ["2026-11-17T09:00:00.000+01:00"], "state": "substitute"}}`,
		`{"Dr. Cy EXAMPLE": {"slots": ["2026-11-17T09:00:00.000+01:00"], "status": "substitute"},
		  "Dr. Di EXAMPLE": {"slots": null, "status": "substitute"}}`,
	}
	answers := map[string]func(http.ResponseWriter){}
	queries := make([]tools.AvailabilityQuery, 0, len(shapes))
	for i, shape := range shapes {
		answers[strconv.Itoa(i+1)] = jsonAnswer(`{"total": 1, "availabilities": [{"date": "2026-11-17",
			"slots": ["2026-11-17T09:00:00.000+01:00"], "substitution": ` + shape + `}]}`)
		queries = append(queries, query(int64(i+1), 1))
	}
	e := newEnv(t)
	e.fake.handle(availabilityPath, availabilityByMotive(answers))

	out, err := e.tools.Availability(t.Context(), tools.AvailabilityInput{Queries: queries, StartDate: "2026-11-17"})
	if err != nil {
		t.Fatal(err)
	}

	for i, shape := range shapes {
		r := out.Results[i]
		if len(r.Substitution) > 0 {
			t.Errorf("shape %d: substitution %+v, want none", i, r.Substitution)
		}
		assertJSON(t, r.Other, `{"total": 1, "availabilities": [{"date": "2026-11-17", "substitution": `+shape+`}]}`)
	}
}

func TestAvailabilityOtherKeepsWhatSlotsDoesNotShow(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fake.handle(availabilityPath, availabilityByMotive(map[string]func(http.ResponseWriter){
		"1": jsonAnswer(`{"total": 3, "availabilities": [
			{"date": "2026-11-17", "slots": [
				"2026-11-17T09:50:00.000+01:00",
				{"start_date": "2026-11-17T12:00:00.000+01:00", "end_date": "2026-11-17T12:15:00.000+01:00",
				 "steps": [{"start_date": "2026-11-17T12:00:00.000+01:00"}, {"start_date": "2026-12-15T12:00:00.000+01:00"}]},
				{"start": "08:00"}],
			 "substitution": null, "appointment_request_slots": [], "some_map": {}},
			{"date": "2026-11-18", "slots": [],
			 "appointment_request_slots": [{"start_date": "2026-11-18T09:00:00.000+01:00"}],
			 "some_new_flag": false, "some_count": 0, "some_note": ""}]}`),
		// The first day's slots can't be read; the second day is fine; the third isn't an object.
		"2": jsonAnswer(`{"total": 2, "availabilities": [
			{"date": "2026-11-17", "slots": "2026-11-17T09:50:00.000+01:00"},
			{"date": "2026-11-18", "slots": ["2026-11-18T10:00:00.000+01:00"]},
			"2026-11-19", null]}`),
		// Not an array: it stays whole.
		"3": jsonAnswer(`{"total": 0, "availabilities": null}`),
	}))

	out, err := e.tools.Availability(t.Context(), tools.AvailabilityInput{
		Queries:   []tools.AvailabilityQuery{query(1, 1), query(2, 1), query(3, 1)},
		StartDate: "2026-11-17",
	})
	if err != nil {
		t.Fatal(err)
	}

	assertJSON(t, out.Results[0].Other, `{"total": 3, "availabilities": [
		{"date": "2026-11-17", "slots": [
			{"start_date": "2026-11-17T12:00:00.000+01:00", "end_date": "2026-11-17T12:15:00.000+01:00",
			 "steps": [{"start_date": "2026-11-17T12:00:00.000+01:00"}, {"start_date": "2026-12-15T12:00:00.000+01:00"}]},
			{"start": "08:00"}]},
		{"date": "2026-11-18", "appointment_request_slots": [{"start_date": "2026-11-18T09:00:00.000+01:00"}],
		 "some_new_flag": false, "some_count": 0, "some_note": ""}]}`)
	assertJSON(t, out.Results[1].Other, `{"total": 2, "availabilities": [
		{"date": "2026-11-17", "slots": "2026-11-17T09:50:00.000+01:00"}, "2026-11-19"]}`)
	assertJSON(t, out.Results[2].Other, `{"total": 0, "availabilities": null}`)
}

func TestAvailabilityBlockedMidBatchKeepsEarlierResults(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fake.handle(availabilityPath, availabilityByMotive(map[string]func(http.ResponseWriter){
		"1": jsonAnswer(`{"total": 0, "availabilities": [], "next_slot": "2027-01-26T09:50:00.000+01:00"}`),
		"2": func(w http.ResponseWriter) { writeHTML(w, http.StatusForbidden, blockPage) },
		"3": jsonAnswer(availabilityWithSlots),
	}))

	_, err := e.tools.Availability(t.Context(), tools.AvailabilityInput{
		Queries: []tools.AvailabilityQuery{query(1, 1), query(2, 1), query(3, 1)},
	})

	text := assertErrorPrefix(t, err, "blocked")
	if !strings.Contains(text, `"visit_motive_id":1`) || !strings.Contains(text, "2027-01-26T09:50:00.000+01:00") {
		t.Errorf("error does not carry the first result: %s", text)
	}
	if n := len(e.fake.doctolibRequests()); n != 2 {
		t.Errorf("sent %d requests, want 2", n)
	}
}

func TestAvailabilityQueryErrorStaysInItsResult(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fake.handle(availabilityPath, availabilityByMotive(map[string]func(http.ResponseWriter){
		"1": jsonAnswer(availabilityWithSlots),
		// "2" is unknown: the fake answers 404.
		"3": jsonAnswer(availabilityWithSlots),
	}))

	out, err := e.tools.Availability(t.Context(), tools.AvailabilityInput{
		Queries: []tools.AvailabilityQuery{query(1, 1), query(2, 1), query(3, 1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Results[1].Status != tools.StatusError || !strings.Contains(out.Results[1].Error, "404") {
		t.Errorf("failed query: %+v", out.Results[1])
	}
	if out.Results[0].Status != tools.StatusHasSlots || out.Results[2].Status != tools.StatusHasSlots {
		t.Errorf("other queries: %q, %q", out.Results[0].Status, out.Results[2].Status)
	}
}

func TestAvailabilityOddSlotFormats(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fake.handle(availabilityPath, availabilityByMotive(map[string]func(http.ResponseWriter){
		// A next slot given as a bare date.
		"1": jsonAnswer(`{"total": 0, "availabilities": [], "next_slot": "2027-01-12"}`),
		// Slots in the window, in shapes the server does not know.
		"2": jsonAnswer(`{"total": 2, "availabilities": [
			{"date": "2026-11-18", "slots": [{"start": "09:00"}, "2026-11-18 09:30"]}],
			"next_slot": "2027-01-12T10:00:00.000+01:00"}`),
	}))

	out, err := e.tools.Availability(t.Context(), tools.AvailabilityInput{
		Queries: []tools.AvailabilityQuery{query(1, 1), query(2, 1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r := out.Results[0]; r.Status != tools.StatusNextSlotKnown {
		t.Errorf("bare-date next slot: status %q", r.Status)
	}
	assertJSON(
		t,
		out.Results[0].Earliest,
		`{"date": "2027-01-12", "time": null, "iso": "2027-01-12", "source": "next_slot"}`,
	)
	// The window has slots, so the next slot after the window is not the earliest one.
	r := out.Results[1]
	if r.Status != tools.StatusHasSlots || r.Earliest != nil {
		t.Errorf("unknown slot shape: status %q, earliest %+v", r.Status, r.Earliest)
	}
	assertJSON(t, r.SlotCount, `2`)
	// slots can't list them, so other is the only place they show.
	assertJSON(t, r.Other, `{"total": 2, "next_slot": "2027-01-12T10:00:00.000+01:00",
		"availabilities": [{"date": "2026-11-18", "slots": [{"start": "09:00"}, "2026-11-18 09:30"]}]}`)
}

func TestAvailabilityNextSlotOfAnotherType(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fake.handle(availabilityPath, availabilityByMotive(map[string]func(http.ResponseWriter){
		"1": jsonAnswer(
			`{"total": 0, "availabilities": [], "next_slot": {"start_date": "2027-01-12T10:00:00.000+01:00"}}`,
		),
	}))

	out, err := e.tools.Availability(t.Context(), tools.AvailabilityInput{
		Queries: []tools.AvailabilityQuery{query(1, 1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Doctolib gives a next slot, so it is not "none visible", even if its shape cannot be read.
	r := out.Results[0]
	if r.Status != tools.StatusNextSlotKnown || r.Earliest != nil {
		t.Errorf("status %q, earliest %+v", r.Status, r.Earliest)
	}
	assertJSON(t, r.Other["next_slot"], `{"start_date": "2027-01-12T10:00:00.000+01:00"}`)
}

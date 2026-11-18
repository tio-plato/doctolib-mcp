package tools_test

import (
	"net/http"
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
		"slots": [{"date": "2026-11-17", "times": ["09:50", "14:00"]}, {"date": "2026-11-19", "times": ["08:30"]}],
		"reason": null,
		"other": {"total": 3, "next_slot": "2026-11-17T09:50:00.000+01:00", "availabilities": [
			{"date": "2026-11-17", "slots": ["2026-11-17T14:00:00.000+01:00", "2026-11-17T09:50:00.000+01:00"]},
			{"date": "2026-11-18", "slots": []},
			{"date": "2026-11-19", "slots": [{"start_date": "2026-11-19T08:30:00.000+01:00", "steps": []}]}]}}`)
	assertJSON(t, out.Results[1], `{"query": {"visit_motive_id": 2, "agenda_ids": [1], "practice_id": 2001},
		"status": "not_opened",
		"earliest": {"date": "2027-01-12", "time": "10:00", "iso": "2027-01-12T10:00:00.000+01:00", "source": "next_slot"},
		"slot_count": 0, "slots": [], "reason": "not_opened_availability",
		"other": {"total": 0, "availabilities": [{"date": "2026-11-17", "slots": []}]}}`)
	assertJSON(t, out.Results[2], `{"query": {"visit_motive_id": 3, "agenda_ids": [1], "practice_id": 2001},
		"status": "next_slot_known",
		"earliest": {"date": "2027-01-26", "time": "09:50", "iso": "2027-01-26T09:50:00.000+01:00", "source": "next_slot"},
		"slot_count": 0, "slots": [], "reason": null,
		"other": {"total": 0, "availabilities": [{"date": "2026-11-17", "slots": []}]}}`)
	assertJSON(t, out.Results[3], `{"query": {"visit_motive_id": 4, "agenda_ids": [1], "practice_id": 2001},
		"status": "none_visible", "earliest": null, "slot_count": 0, "slots": [], "reason": null,
		"other": {"total": 0, "availabilities": [{"date": "2026-11-17", "slots": []}]}}`)
	if r := out.Results[4]; r.Status != tools.StatusNoneVisible || r.Reason == nil || *r.Reason != "some_new_reason" {
		t.Errorf("unknown reason: status %q, reason %v", r.Status, r.Reason)
	}
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
		// A slot in the window, in a shape the server does not know.
		"2": jsonAnswer(`{"total": 1, "availabilities": [{"date": "2026-11-18", "slots": [{"start": "09:00"}]}],
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
	// The window has a slot, so the next slot after the window is not the earliest one.
	r := out.Results[1]
	if r.Status != tools.StatusHasSlots || r.Earliest != nil {
		t.Errorf("unknown slot shape: status %q, earliest %+v", r.Status, r.Earliest)
	}
	assertJSON(t, r.SlotCount, `1`)
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

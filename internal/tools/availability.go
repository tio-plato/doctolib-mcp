package tools

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tio-plato/doctolib-mcp/internal/gate"
)

const (
	availabilityPath = "/availabilities.json"
	// windowDays is the most Doctolib returns in one request.
	windowDays = 15
	// MaxQueries is one page of search results.
	MaxQueries = 20
	dateLayout = "2006-01-02"
)

const (
	StatusHasSlots      = "has_slots"
	StatusNotOpened     = "not_opened"
	StatusNextSlotKnown = "next_slot_known"
	StatusNoneVisible   = "none_visible"
	StatusError         = "error"
)

type AvailabilityInput struct {
	Queries   []AvailabilityQuery `json:"queries"              jsonschema:"1 to 20 availability_query objects, as search_practitioners or practitioner_profile give them"`
	StartDate string              `json:"start_date,omitempty" jsonschema:"first day of the 15-day window, YYYY-MM-DD; default today in Europe/Paris"`
}

type AvailabilityOutput struct {
	Window  Window               `json:"window"`
	Results []AvailabilityResult `json:"results"`
}

type Window struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type AvailabilityResult struct {
	Query        AvailabilityQuery `json:"query"`
	Status       string            `json:"status"`
	Earliest     *Earliest         `json:"earliest"`
	SlotCount    *int              `json:"slot_count,omitzero"`
	Slots        string            `json:"slots"`
	Substitution []Substitution    `json:"substitution,omitempty"`
	Reason       *string           `json:"reason"`
	Other        map[string]any    `json:"other,omitzero"`
	Error        string            `json:"error,omitempty"`
}

type Earliest struct {
	Date   string  `json:"date"`
	Time   *string `json:"time"`
	ISO    string  `json:"iso"`
	Source string  `json:"source"`
}

// Substitution is one day's times that Doctolib marks for another practitioner.
type Substitution struct {
	Day          string `json:"day"`
	Practitioner string `json:"practitioner"`
	Status       string `json:"status"`
	Times        string `json:"times"`
}

// partialError carries the results fetched before a block or a reached cap.
type partialError struct {
	err  error
	done *AvailabilityOutput
}

func (e *partialError) Error() string { return e.err.Error() }

func (e *partialError) Unwrap() error { return e.err }

func (t *Tools) Availability(ctx context.Context, in AvailabilityInput) (*AvailabilityOutput, error) {
	if len(in.Queries) == 0 || len(in.Queries) > MaxQueries {
		return nil, fmt.Errorf(
			"queries: got %d, want 1 to %d",
			len(in.Queries),
			MaxQueries,
		)
	}
	start, err := t.startDate(in.StartDate)
	if err != nil {
		return nil, err
	}
	out := &AvailabilityOutput{
		Window: Window{
			Start: start.Format(dateLayout),
			End:   start.AddDate(0, 0, windowDays-1).Format(dateLayout),
		},
		Results: []AvailabilityResult{},
	}
	for _, q := range in.Queries {
		raw, getErr := t.doctolib.Get(ctx, availabilityPath, availabilityParams(out.Window.Start, q))
		if getErr != nil {
			var blocked *gate.BlockedError
			var budget *gate.BudgetError
			if errors.As(getErr, &blocked) || errors.As(getErr, &budget) || ctx.Err() != nil {
				return nil, &partialError{err: fmt.Errorf("read availabilities: %w", getErr), done: out}
			}
			out.Results = append(out.Results, failed(q, getErr))
			continue
		}
		out.Results = append(out.Results, organiseAvailability(raw, q))
	}
	return out, nil
}

func (t *Tools) today() time.Time {
	now := t.now().In(t.paris)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, t.paris)
}

func (t *Tools) startDate(s string) (time.Time, error) {
	if s == "" {
		return t.today(), nil
	}
	d, err := time.ParseInLocation(dateLayout, s, t.paris)
	if err != nil {
		return time.Time{}, fmt.Errorf("start_date %q: want YYYY-MM-DD: %w", s, err)
	}
	return d, nil
}

// availabilityParams matches the website's request: same parameters, same order.
func availabilityParams(start string, q AvailabilityQuery) string {
	agendas := make([]string, 0, len(q.AgendaIDs))
	for _, a := range q.AgendaIDs {
		agendas = append(agendas, strconv.FormatInt(a, 10))
	}
	return "start_date=" + start +
		"&visit_motive_ids=" + strconv.FormatInt(q.VisitMotiveID, 10) +
		"&agenda_ids=" + strings.Join(agendas, "-") +
		"&practice_ids=" + strconv.FormatInt(q.PracticeID, 10) +
		"&limit=" + strconv.Itoa(windowDays)
}

func failed(q AvailabilityQuery, err error) AvailabilityResult {
	return AvailabilityResult{
		Query: q, Status: StatusError, Earliest: nil, SlotCount: nil, Slots: "", Substitution: nil, Reason: nil,
		Other: nil, Error: err.Error(),
	}
}

type slot struct {
	at  time.Time
	iso string
}

func organiseAvailability(raw []byte, q AvailabilityQuery) AvailabilityResult {
	o := parseObject(raw)
	if o == nil {
		return failed(q, fmt.Errorf("decode availability response: %w", errNotObject))
	}
	r := AvailabilityResult{Query: q, Status: "", Earliest: nil, Reason: nil, Error: ""}

	// other.availabilities keeps only what slots and substitution don't show. Not an array, null included, it stays
	// whole.
	var days []json.RawMessage
	if o.peek("availabilities", &days) && days != nil {
		o.drop("availabilities")
	}
	c := collected{count: 0, first: nil, slots: byDay{}, subs: map[subKey]byDay{}}
	var left []any
	for _, d := range days {
		if l := c.day(d); l != nil {
			left = append(left, l)
		}
	}
	count, first := c.count, c.first
	r.SlotCount = &count
	r.Slots = c.slots.String()
	r.Substitution = c.substitution()

	o.take("reason", &r.Reason)
	// A next_slot of any type counts as given; only a string can be read.
	var nextRaw json.RawMessage
	given := o.peek("next_slot", &nextRaw) && string(nextRaw) != "null"
	var next string
	o.peek("next_slot", &next)
	switch {
	case count > 0:
		r.Status = StatusHasSlots
	case r.Reason != nil && *r.Reason == "not_opened_availability":
		r.Status = StatusNotOpened
	case given:
		r.Status = StatusNextSlotKnown
	default:
		r.Status = StatusNoneVisible
	}

	// next_slot is the earliest only when the window has no slot at all, not when its slots have an unknown shape.
	switch {
	case first != nil:
		r.Earliest = earliest(*first, "window")
	case count == 0 && given:
		if e := nextSlot(next); e != nil {
			o.drop("next_slot")
			r.Earliest = e
		}
	}
	r.Other = o.rest()
	if len(left) > 0 {
		r.Other["availabilities"] = left
	}
	return r
}

// collected gathers what slots and substitution show, across the days.
type collected struct {
	count int
	first *slot
	slots byDay
	subs  map[subKey]byDay
}

type subKey struct {
	practitioner string
	status       string
}

// day returns what slots and substitution don't show of one day, or nil when nothing but its date is left.
func (c *collected) day(raw json.RawMessage) any {
	d := parseObject(raw)
	if d == nil {
		if string(raw) == "null" {
			return nil
		}
		return raw
	}
	var slots, unshown []json.RawMessage
	// Slots that aren't an array stay in the day, uncounted.
	if d.take("slots", &slots) {
		unshown = c.addSlots(slots)
	}
	var sub json.RawMessage
	if d.peek("substitution", &sub) && c.addSubstitution(sub) {
		d.drop("substitution")
	}
	left := map[string]any{}
	for k, v := range d.fields {
		if !empty(v) {
			left[k] = v
		}
	}
	if len(unshown) > 0 {
		left["slots"] = unshown
	}
	n := len(left)
	if _, ok := left["date"]; ok {
		n--
	}
	if n == 0 {
		return nil
	}
	return left
}

// addSlots counts and lists a day's slots, and returns those slots doesn't show in full.
func (c *collected) addSlots(slots []json.RawMessage) []json.RawMessage {
	var unshown []json.RawMessage
	for _, raw := range slots {
		c.count++
		s, ok := parseSlot(raw)
		if ok {
			c.slots.add(s.at)
			if c.first == nil || s.at.Before(c.first.at) {
				c.first = &s
			}
		}
		if !ok || !onlyTime(raw) {
			unshown = append(unshown, raw)
		}
	}
	return unshown
}

// onlyTime is true for an ISO string, and for an object whose fields other than start_date are all empty.
func onlyTime(raw json.RawMessage) bool {
	var iso string
	if json.Unmarshal(raw, &iso) == nil {
		return true
	}
	o := parseObject(raw)
	if o == nil {
		return false
	}
	o.drop("start_date")
	for _, v := range o.fields {
		if !empty(v) {
			return false
		}
	}
	return true
}

// empty is true for null, [] and {}; false, 0 and "" are values.
func empty(raw json.RawMessage) bool {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	switch v := v.(type) {
	case nil:
		return true
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	default:
		return false
	}
}

// addSubstitution takes a day's substitution only when every entry has the shape seen on Doctolib,
// {"<practitioner>": {"slots": [ISO time…], "status": "…"}}, and reports whether it did.
func (c *collected) addSubstitution(raw json.RawMessage) bool {
	var byName map[string]json.RawMessage
	if json.Unmarshal(raw, &byName) != nil || byName == nil {
		return false
	}
	type entry struct {
		key subKey
		ats []time.Time
	}
	entries := make([]entry, 0, len(byName))
	for name, v := range byName {
		ats, status, ok := substitutionEntry(v)
		if !ok {
			return false
		}
		entries = append(entries, entry{key: subKey{practitioner: name, status: status}, ats: ats})
	}
	for _, e := range entries {
		if c.subs[e.key] == nil {
			c.subs[e.key] = byDay{}
		}
		for _, at := range e.ats {
			c.subs[e.key].add(at)
		}
	}
	return true
}

// substitutionEntry reads {"slots": [ISO time…], "status": "…"}, with no other field.
func substitutionEntry(raw json.RawMessage) ([]time.Time, string, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 2 {
		return nil, "", false
	}
	// Pointers, so that null or a missing field isn't read as an empty value.
	var isos *[]string
	var status *string
	if json.Unmarshal(fields["slots"], &isos) != nil || isos == nil ||
		json.Unmarshal(fields["status"], &status) != nil || status == nil {
		return nil, "", false
	}
	ats := make([]time.Time, 0, len(*isos))
	for _, iso := range *isos {
		at, err := time.Parse(time.RFC3339, iso)
		if err != nil {
			return nil, "", false
		}
		ats = append(ats, at)
	}
	return ats, *status, true
}

// substitution lists one entry per day and practitioner, by date, then practitioner.
func (c *collected) substitution() []Substitution {
	type dated struct {
		date string
		sub  Substitution
	}
	var all []dated
	for k, days := range c.subs {
		for _, d := range days {
			all = append(all, dated{date: d.date, sub: Substitution{
				Day: d.label, Practitioner: k.practitioner, Status: k.status, Times: d.merged(),
			}})
		}
	}
	slices.SortFunc(all, func(a, b dated) int {
		return cmp.Or(
			strings.Compare(a.date, b.date),
			strings.Compare(a.sub.Practitioner, b.sub.Practitioner),
			strings.Compare(a.sub.Status, b.sub.Status),
		)
	})
	out := make([]Substitution, 0, len(all))
	for _, a := range all {
		out = append(out, a.sub)
	}
	return out
}

const (
	// dayLayout gives the weekday too, which models often get wrong from a date.
	dayLayout      = "Mon 2006-01-02"
	minutesPerHour = 60
	// minRun is the fewest evenly spaced times written as one run.
	minRun = 3
)

// byDay holds clock times under the date of their ISO time, in the offset Doctolib writes it in.
type byDay map[string]*dayTimes

type dayTimes struct {
	date    string
	label   string
	minutes []int
}

func (b byDay) add(at time.Time) {
	date := at.Format(dateLayout)
	d := b[date]
	if d == nil {
		d = &dayTimes{date: date, label: at.Format(dayLayout), minutes: nil}
		b[date] = d
	}
	d.minutes = append(d.minutes, at.Hour()*minutesPerHour+at.Minute())
}

// String writes the days in date order, separated by "; ", each as its label and its merged times.
func (b byDay) String() string {
	days := slices.SortedFunc(maps.Values(b), func(x, y *dayTimes) int { return strings.Compare(x.date, y.date) })
	parts := make([]string, 0, len(days))
	for _, d := range days {
		parts = append(parts, d.label+" "+d.merged())
	}
	return strings.Join(parts, "; ")
}

// merged writes the times in order, without repeats. From the earliest time not yet written, greedily: a run of at
// least minRun times at the same step becomes first-last/step, both ends included; otherwise the first time alone.
func (d *dayTimes) merged() string {
	m := slices.Compact(slices.Sorted(slices.Values(d.minutes)))
	parts := make([]string, 0, len(m))
	for i := 0; i < len(m); {
		n := runLength(m[i:])
		if n >= minRun {
			parts = append(parts, fmt.Sprintf("%s-%s/%d", clock(m[i]), clock(m[i+n-1]), m[i+1]-m[i]))
			i += n
			continue
		}
		parts = append(parts, clock(m[i]))
		i++
	}
	return strings.Join(parts, " ")
}

// runLength counts the times from the first that keep the step between the first two.
func runLength(m []int) int {
	if len(m) < minRun {
		return len(m)
	}
	n := 2
	for n < len(m) && m[n]-m[n-1] == m[1]-m[0] {
		n++
	}
	return n
}

func clock(minutes int) string {
	return fmt.Sprintf("%02d:%02d", minutes/minutesPerHour, minutes%minutesPerHour)
}

// parseSlot accepts a slot as an ISO string or as an object with start_date.
func parseSlot(raw json.RawMessage) (slot, bool) {
	var iso string
	if json.Unmarshal(raw, &iso) != nil {
		var obj struct {
			StartDate string `json:"start_date"`
		}
		if json.Unmarshal(raw, &obj) != nil {
			return slot{}, false
		}
		iso = obj.StartDate
	}
	at, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return slot{}, false
	}
	return slot{at: at, iso: iso}, true
}

// earliest keeps the offset the ISO time is written in, Paris time from Doctolib.
func earliest(s slot, source string) *Earliest {
	hm := s.at.Format("15:04")
	return &Earliest{Date: s.at.Format(dateLayout), Time: &hm, ISO: s.iso, Source: source}
}

// nextSlot accepts a full ISO time or a bare date.
func nextSlot(raw string) *Earliest {
	if at, err := time.Parse(time.RFC3339, raw); err == nil {
		return earliest(slot{at: at, iso: raw}, "next_slot")
	}
	if day, err := time.Parse(dateLayout, raw); err == nil {
		return &Earliest{Date: day.Format(dateLayout), Time: nil, ISO: raw, Source: "next_slot"}
	}
	return nil
}

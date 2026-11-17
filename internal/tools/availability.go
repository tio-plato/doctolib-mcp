package tools

import (
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

// Slot statuses, from the first rule that matches.
const (
	StatusHasSlots      = "has_slots"
	StatusNotOpened     = "not_opened"
	StatusNextSlotKnown = "next_slot_known"
	StatusNoneVisible   = "none_visible"
	StatusError         = "error"
)

// AvailabilityInput is the input of availability.
type AvailabilityInput struct {
	Queries   []AvailabilityQuery `json:"queries"              jsonschema:"1 to 20 availability_query objects, as search_practitioners or practitioner_profile give them"`
	StartDate string              `json:"start_date,omitempty" jsonschema:"first day of the 15-day window, YYYY-MM-DD; default today in Europe/Paris"`
}

// AvailabilityOutput is the result of availability.
type AvailabilityOutput struct {
	Window  Window               `json:"window"`
	Results []AvailabilityResult `json:"results"`
}

// Window is the 15 days asked for, both ends included.
type Window struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// AvailabilityResult is the outcome for one query; a failed query has only query, status "error" and error.
type AvailabilityResult struct {
	Query     AvailabilityQuery          `json:"query"`
	Status    string                     `json:"status"`
	Earliest  *Earliest                  `json:"earliest"`
	SlotCount *int                       `json:"slot_count,omitzero"`
	Slots     []Day                      `json:"slots,omitzero"`
	Reason    *string                    `json:"reason"`
	Other     map[string]json.RawMessage `json:"other,omitzero"`
	Error     string                     `json:"error,omitempty"`
}

// Earliest is the first free slot: in the window, else Doctolib's next_slot.
type Earliest struct {
	Date   string `json:"date"`
	Time   string `json:"time"`
	ISO    string `json:"iso"`
	Source string `json:"source"`
}

// Day lists the free times of one day, in order.
type Day struct {
	Date  string   `json:"date"`
	Times []string `json:"times"`
}

// partialError stops a batch and carries the results fetched before it.
type partialError struct {
	err  error
	done *AvailabilityOutput
}

func (e *partialError) Error() string { return e.err.Error() }

func (e *partialError) Unwrap() error { return e.err }

var errQueryCount = fmt.Errorf("queries: want 1 to %d", MaxQueries)

// Availability runs availability: one Doctolib request per query, in turn, at the gate's pace.
//
// A failed query is reported in its own result. A block or a reached cap stops
// the batch: the error then carries the results fetched so far.
func (t *Tools) Availability(ctx context.Context, in AvailabilityInput) (*AvailabilityOutput, error) {
	if len(in.Queries) == 0 || len(in.Queries) > MaxQueries {
		return nil, errQueryCount
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

func (t *Tools) startDate(s string) (time.Time, error) {
	if s == "" {
		now := t.now().In(t.paris)
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, t.paris), nil
	}
	d, err := time.ParseInLocation(dateLayout, s, t.paris)
	if err != nil {
		return time.Time{}, fmt.Errorf("start_date %q: want YYYY-MM-DD: %w", s, err)
	}
	return d, nil
}

// availabilityParams are the five parameters, in the order, of the request the web front end sends.
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
		Query: q, Status: StatusError, Earliest: nil, SlotCount: nil, Slots: nil, Reason: nil, Other: nil,
		Error: err.Error(),
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

	// The days stay whole in other.availabilities, with the full ISO times.
	var days []struct {
		Slots []json.RawMessage `json:"slots"`
	}
	o.peek("availabilities", &days)
	count := 0
	var first *slot
	times := map[string][]string{}
	for _, d := range days {
		for _, raw := range d.Slots {
			count++
			s, ok := parseSlot(raw)
			if !ok {
				continue
			}
			date := s.at.Format(dateLayout)
			times[date] = append(times[date], s.at.Format("15:04"))
			if first == nil || s.at.Before(first.at) {
				first = &s
			}
		}
	}
	r.SlotCount = &count
	r.Slots = make([]Day, 0, len(times))
	for _, date := range slices.Sorted(maps.Keys(times)) {
		slices.Sort(times[date])
		r.Slots = append(r.Slots, Day{Date: date, Times: times[date]})
	}

	o.take("reason", &r.Reason)
	var next *string
	o.peek("next_slot", &next)
	switch {
	case count > 0:
		r.Status = StatusHasSlots
	case r.Reason != nil && *r.Reason == "not_opened_availability":
		r.Status = StatusNotOpened
	case next != nil:
		r.Status = StatusNextSlotKnown
	default:
		r.Status = StatusNoneVisible
	}

	if first != nil {
		r.Earliest = earliest(*first, "window")
	} else if next != nil {
		if at, err := time.Parse(time.RFC3339, *next); err == nil {
			o.drop("next_slot")
			r.Earliest = earliest(slot{at: at, iso: *next}, "next_slot")
		}
	}
	r.Other = o.rest()
	return r
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

// earliest keeps the local date and time the ISO time is written in (Paris time from Doctolib).
func earliest(s slot, source string) *Earliest {
	return &Earliest{Date: s.at.Format(dateLayout), Time: s.at.Format("15:04"), ISO: s.iso, Source: source}
}

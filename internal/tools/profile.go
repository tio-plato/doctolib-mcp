package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

const profilePath = "/online_booking/api/slot_selection_funnel/v1/info.json"

// ProfileInput is the input of practitioner_profile.
type ProfileInput struct {
	ProfileURL string `json:"profile_url" jsonschema:"profile_url of a search_practitioners result"`
}

// ProfileOutput is the result of practitioner_profile.
type ProfileOutput struct {
	Name               *string               `json:"name"`
	Speciality         *string               `json:"speciality"`
	TelehealthEnabled  *bool                 `json:"telehealth_enabled"`
	ProfileURL         string                `json:"profile_url"`
	Places             []ProfilePlace        `json:"places"`
	Practitioners      []ProfilePractitioner `json:"practitioners"`
	Motives            []Motive              `json:"motives"`
	UnavailableAgendas []UnavailableAgenda   `json:"unavailable_agendas"`
	Other              map[string]any        `json:"other"`
}

// ProfilePlace is one practice of the profile.
type ProfilePlace struct {
	PracticeID *int64         `json:"practice_id"`
	Name       *string        `json:"name"`
	Address    *string        `json:"address"`
	Zipcode    *string        `json:"zipcode"`
	City       *string        `json:"city"`
	Lat        *float64       `json:"lat"`
	Lng        *float64       `json:"lng"`
	Phone      *string        `json:"phone"`
	Other      map[string]any `json:"other"`
}

// ProfilePractitioner is one practitioner of the profile; an organisation often has several.
type ProfilePractitioner struct {
	ID    *int64         `json:"id"`
	Name  *string        `json:"name"`
	Other map[string]any `json:"other"`
}

// Motive is one visit motive and where it is offered.
type Motive struct {
	ID         *int64  `json:"id"`
	Name       *string `json:"name"`
	Telehealth *bool   `json:"telehealth"`
	Category   *string `json:"category"`
	// NewPatientRestrictions is Doctolib's value as it is.
	NewPatientRestrictions json.RawMessage `json:"new_patient_restrictions"`
	Offered                []Offer         `json:"offered"`
	Other                  map[string]any  `json:"other"`
}

// Offer is one (practice, agenda) that offers a visit motive.
type Offer struct {
	PracticeID   int64   `json:"practice_id"`
	Practitioner *string `json:"practitioner"`
	AgendaID     int64   `json:"agenda_id"`
	// NewPatientRestrictions holds the motive's restriction entries that name this agenda, copied as they are.
	NewPatientRestrictions []json.RawMessage `json:"new_patient_restrictions"`
	AvailabilityQuery      AvailabilityQuery `json:"availability_query"`
}

// UnavailableAgenda is an agenda whose booking is temporarily disabled.
type UnavailableAgenda struct {
	AgendaID     int64   `json:"agenda_id"`
	PracticeID   *int64  `json:"practice_id"`
	Practitioner *string `json:"practitioner"`
}

var errNoSlug = errors.New("no profile slug in the URL path")

// Profile runs practitioner_profile: one Doctolib request.
func (t *Tools) Profile(ctx context.Context, in ProfileInput) (*ProfileOutput, error) {
	slug, err := profileSlug(in.ProfileURL)
	if err != nil {
		return nil, err
	}
	raw, err := t.doctolib.Get(ctx, profilePath, "profile_slug="+url.QueryEscape(slug)+"&locale=fr")
	if err != nil {
		return nil, fmt.Errorf("read profile: %w", err)
	}
	return organiseProfile(raw, in.ProfileURL)
}

// profileSlug is the last segment of the URL path, without the query.
func profileSlug(profileURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(profileURL))
	if err != nil {
		return "", fmt.Errorf("parse profile_url %q: %w", profileURL, err)
	}
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	slug := segments[len(segments)-1]
	if slug == "" {
		return "", fmt.Errorf("profile_url %q: %w", profileURL, errNoSlug)
	}
	return slug, nil
}

type agenda struct {
	ID                       int64              `json:"id"`
	BookingTemporaryDisabled bool               `json:"booking_temporary_disabled"`
	PracticeID               *int64             `json:"practice_id"`
	PractitionerID           *int64             `json:"practitioner_id"`
	MotivesByPractice        map[string][]int64 `json:"visit_motive_ids_by_practice_id"`
}

func organiseProfile(raw []byte, profileURL string) (*ProfileOutput, error) {
	top := parseObject(raw)
	if top == nil {
		return nil, fmt.Errorf("decode profile response: %w", errNotObject)
	}
	// The fields sit in a data wrapper; what is left of them goes to the top of other.
	data := top
	if d := parseObject(top.fields["data"]); d != nil && len(d.fields) > 0 {
		top.drop("data")
		data = d
	}
	out := &ProfileOutput{
		Name: nil, Speciality: nil, TelehealthEnabled: nil, ProfileURL: profileURL,
		Places: []ProfilePlace{}, Practitioners: []ProfilePractitioner{}, Motives: []Motive{},
		UnavailableAgendas: []UnavailableAgenda{}, Other: nil,
	}
	if prof := data.sub("profile"); prof != nil {
		prof.take("name_with_title", &out.Name)
		prof.take("telehealth_enabled", &out.TelehealthEnabled)
		if sp := prof.sub("speciality"); sp != nil {
			sp.take("name", &out.Speciality)
		}
	}
	var places, practitioners, motives []json.RawMessage
	if data.take("places", &places) {
		for _, p := range places {
			out.Places = append(out.Places, organisePlace(p))
		}
	}
	if data.take("practitioners", &practitioners) {
		for _, p := range practitioners {
			out.Practitioners = append(out.Practitioners, organiseProfilePractitioner(p))
		}
	}
	// Agendas and categories are read to organise the motives; they stay whole in other.
	ctx := motiveContext{agendas: nil, names: practitionerNames(out.Practitioners), categories: categoryNames(data)}
	data.peek("agendas", &ctx.agendas)
	if data.take("visit_motives", &motives) {
		for _, m := range motives {
			out.Motives = append(out.Motives, organiseMotive(m, ctx))
		}
	}
	for _, a := range ctx.agendas {
		if a.BookingTemporaryDisabled {
			out.UnavailableAgendas = append(out.UnavailableAgendas, UnavailableAgenda{
				AgendaID: a.ID, PracticeID: a.PracticeID, Practitioner: ctx.practitioner(a),
			})
		}
	}
	out.Other = data.rest()
	if data != top {
		out.Other = unwrap(out.Other, top.rest())
	}
	return out, nil
}

// unwrap puts what is left of the data wrapper next to what is left inside
// it. When both levels have a field of the same name, it keeps Doctolib's
// nesting instead, so neither is lost.
func unwrap(inner, wrapper map[string]any) map[string]any {
	for k := range wrapper {
		if _, clash := inner[k]; clash {
			wrapper["data"] = inner
			return wrapper
		}
	}
	maps.Copy(inner, wrapper)
	return inner
}

// motiveContext is what organising a motive needs from the rest of the profile.
type motiveContext struct {
	agendas    []agenda
	names      map[int64]*string
	categories map[int64]string
}

// practitioner is the name of the agenda's practitioner, or nil.
func (c motiveContext) practitioner(a agenda) *string {
	if a.PractitionerID == nil {
		return nil
	}
	return c.names[*a.PractitionerID]
}

func practitionerNames(practitioners []ProfilePractitioner) map[int64]*string {
	names := map[int64]*string{}
	for _, p := range practitioners {
		if p.ID != nil {
			names[*p.ID] = p.Name
		}
	}
	return names
}

func categoryNames(data *object) map[int64]string {
	var categories []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	data.peek("visit_motive_categories", &categories)
	names := map[int64]string{}
	for _, c := range categories {
		names[c.ID] = c.Name
	}
	return names
}

func organisePlace(raw json.RawMessage) ProfilePlace {
	var p ProfilePlace
	o := parseObject(raw)
	if o == nil {
		p.Other = notObject(raw)
		return p
	}
	var ids []int64
	if o.peek("practice_ids", &ids) && len(ids) == 1 {
		o.drop("practice_ids")
		p.PracticeID = &ids[0]
	}
	o.take("name", &p.Name)
	o.take("address", &p.Address)
	o.take("zipcode", &p.Zipcode)
	o.take("city", &p.City)
	o.take("latitude", &p.Lat)
	o.take("longitude", &p.Lng)
	o.take("landline_number", &p.Phone)
	p.Other = o.rest()
	return p
}

func organiseProfilePractitioner(raw json.RawMessage) ProfilePractitioner {
	var p ProfilePractitioner
	o := parseObject(raw)
	if o == nil {
		p.Other = notObject(raw)
		return p
	}
	o.take("id", &p.ID)
	o.take("name_with_title", &p.Name)
	p.Other = o.rest()
	return p
}

func organiseMotive(raw json.RawMessage, ctx motiveContext) Motive {
	m := Motive{Offered: []Offer{}}
	o := parseObject(raw)
	if o == nil {
		m.Other = notObject(raw)
		return m
	}
	o.take("id", &m.ID)
	o.take("name", &m.Name)
	o.take("telehealth", &m.Telehealth)
	var categoryID *int64
	if o.peek("visit_motive_category_id", &categoryID) && categoryID != nil {
		if name, ok := ctx.categories[*categoryID]; ok {
			m.Category = &name
		}
	}
	o.take("new_patient_restrictions", &m.NewPatientRestrictions)
	m.Other = o.rest()
	if m.ID == nil {
		return m
	}
	var restrictions []json.RawMessage
	_ = json.Unmarshal(m.NewPatientRestrictions, &restrictions)
	for _, a := range ctx.agendas {
		if a.BookingTemporaryDisabled {
			continue
		}
		for _, practice := range practicesOffering(a, *m.ID) {
			m.Offered = append(m.Offered, Offer{
				PracticeID:             practice,
				Practitioner:           ctx.practitioner(a),
				AgendaID:               a.ID,
				NewPatientRestrictions: restrictionsNaming(restrictions, a.ID),
				AvailabilityQuery: AvailabilityQuery{
					VisitMotiveID: *m.ID,
					AgendaIDs:     []int64{a.ID},
					PracticeID:    practice,
				},
			})
		}
	}
	return m
}

// practicesOffering lists, in ascending order, the practices where the agenda offers the motive.
func practicesOffering(a agenda, motive int64) []int64 {
	var out []int64
	for key, motives := range a.MotivesByPractice {
		practice, err := strconv.ParseInt(key, 10, 64)
		if err == nil && slices.Contains(motives, motive) {
			out = append(out, practice)
		}
	}
	slices.Sort(out)
	return out
}

// restrictionsNaming returns the entries whose agenda_ids name the agenda, as they are.
func restrictionsNaming(restrictions []json.RawMessage, agendaID int64) []json.RawMessage {
	out := []json.RawMessage{}
	for _, r := range restrictions {
		var entry struct {
			AgendaIDs []int64 `json:"agenda_ids"`
		}
		if json.Unmarshal(r, &entry) == nil && slices.Contains(entry.AgendaIDs, agendaID) {
			out = append(out, r)
		}
	}
	return out
}

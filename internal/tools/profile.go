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
	"sync"
	"time"
	"unicode/utf8"
)

const profilePath = "/online_booking/api/slot_selection_funnel/v1/info.json"

// profileBudget caps a result, in characters of its JSON. The tightest usual clients are Codex, which cuts a result
// past about 40,000 bytes, and Gemini CLI, which puts one past 40,000 characters in a file: this leaves them 10%.
const profileBudget = 36000

// A kept profile lets an agent page through a large centre's motives with one Doctolib request.
const (
	profileKeep  = 10 * time.Minute
	profilesKept = 20
)

type ProfileInput struct {
	ProfileURL string  `json:"profile_url"          jsonschema:"profile_url of a search_practitioners result"`
	MotiveIDs  []int64 `json:"motive_ids,omitempty" jsonschema:"motive ids from this profile's motive_index or motives; returns those that fit in one result, with the agendas and practitioners that offer them"`
}

type ProfileOutput struct {
	Name              *string               `json:"name"`
	Speciality        *string               `json:"speciality"`
	TelehealthEnabled *bool                 `json:"telehealth_enabled"`
	ProfileURL        string                `json:"profile_url"`
	Places            []ProfilePlace        `json:"places"`
	Practitioners     []ProfilePractitioner `json:"practitioners"`
	Agendas           []ProfileAgenda       `json:"agendas"`
	Motives           []Motive              `json:"motives"`
	// MotiveIndex stands in for the motives of a profile too large for one result. It is a pointer so that an
	// index of no motive still shows.
	MotiveIndex *[]MotiveSummary `json:"motive_index,omitempty"`
	// RemainingMotiveIDs are the motive_ids asked for that didn't fit.
	RemainingMotiveIDs []int64        `json:"remaining_motive_ids,omitempty"`
	Other              map[string]any `json:"other"`
}

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

type ProfilePractitioner struct {
	ID    *int64         `json:"id"`
	Name  *string        `json:"name"`
	Other map[string]any `json:"other"`
}

type ProfileAgenda struct {
	AgendaID                 *int64         `json:"agenda_id"`
	PracticeID               *int64         `json:"practice_id"`
	PractitionerID           *int64         `json:"practitioner_id"`
	Practitioner             *string        `json:"practitioner"`
	BookingTemporaryDisabled *bool          `json:"booking_temporary_disabled"`
	Other                    map[string]any `json:"other"`
}

type Motive struct {
	ID                      *int64              `json:"id"`
	Name                    *string             `json:"name"`
	Telehealth              *bool               `json:"telehealth"`
	Category                *string             `json:"category"`
	NewPatientRestrictions  json.RawMessage     `json:"new_patient_restrictions"`
	AgeReferralRestrictions json.RawMessage     `json:"age_referral_restrictions"`
	Offered                 []AvailabilityQuery `json:"offered"`
	Other                   map[string]any      `json:"other"`
}

type MotiveSummary struct {
	ID         *int64  `json:"id"`
	Name       *string `json:"name"`
	Category   *string `json:"category"`
	Telehealth *bool   `json:"telehealth"`
}

var errNoSlug = errors.New("no profile slug in the URL path")

func (t *Tools) Profile(ctx context.Context, in ProfileInput) (*ProfileOutput, error) {
	slug, err := profileSlug(in.ProfileURL)
	if err != nil {
		return nil, err
	}
	raw, err := t.profileResponse(ctx, slug)
	if err != nil {
		return nil, err
	}
	p, err := organiseProfile(raw, in.ProfileURL)
	if err != nil {
		return nil, err
	}
	return p.result(in.MotiveIDs)
}

// profileResponse sends no request for a kept profile, but still stops at the block marker: every Doctolib tool
// stays off while it is there.
func (t *Tools) profileResponse(ctx context.Context, slug string) ([]byte, error) {
	if raw, ok := t.profiles.get(slug, t.now()); ok {
		err := t.doctolib.Gate().CheckMarker()
		if err != nil {
			return nil, fmt.Errorf("read profile: %w", err)
		}
		return raw, nil
	}
	raw, err := t.doctolib.Get(ctx, profilePath, "profile_slug="+url.QueryEscape(slug)+"&locale=fr")
	if err != nil {
		return nil, fmt.Errorf("read profile: %w", err)
	}
	t.profiles.put(slug, raw, t.now())
	return raw, nil
}

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

// profileStore keeps Doctolib's responses by profile slug, in this process's memory only.
type profileStore struct {
	mu   sync.Mutex
	kept map[string]keptProfile
}

type keptProfile struct {
	raw     []byte
	fetched time.Time
}

func newProfileStore() *profileStore {
	return &profileStore{mu: sync.Mutex{}, kept: map[string]keptProfile{}}
}

func (s *profileStore) get(slug string, now time.Time) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.kept[slug]
	if !ok || now.Sub(k.fetched) >= profileKeep {
		return nil, false
	}
	return k.raw, true
}

// put makes room by dropping the profile fetched first.
func (s *profileStore) put(slug string, raw []byte, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.kept[slug]; !ok && len(s.kept) >= profilesKept {
		oldest := slices.MinFunc(slices.Collect(maps.Keys(s.kept)), func(a, b string) int {
			return s.kept[a].fetched.Compare(s.kept[b].fetched)
		})
		delete(s.kept, oldest)
	}
	s.kept[slug] = keptProfile{raw: raw, fetched: now}
}

// organisedProfile is the whole profile, before it is cut to fit a result.
type organisedProfile struct {
	out *ProfileOutput
	// agendas holds out.Agendas in the same order, with the motives each lists.
	agendas []agenda
}

func organiseProfile(raw []byte, profileURL string) (*organisedProfile, error) {
	top := parseObject(raw)
	if top == nil {
		return nil, fmt.Errorf("decode profile response: %w", errNotObject)
	}
	// The fields sit in a data wrapper; what is left of them goes to the top of other.
	data := top
	if d := parseObject(top.fields["data"]); d != nil {
		data = d
		// An empty wrapper stays in other.
		if len(d.fields) > 0 {
			top.drop("data")
		}
	}
	out := &ProfileOutput{
		Name: nil, Speciality: nil, TelehealthEnabled: nil, ProfileURL: profileURL,
		Places: nil, Practitioners: nil, Agendas: []ProfileAgenda{}, Motives: nil,
		MotiveIndex: nil, RemainingMotiveIDs: nil, Other: nil,
	}
	if prof := data.sub("profile"); prof != nil {
		prof.take("name_with_title", &out.Name)
		prof.take("telehealth_enabled", &out.TelehealthEnabled)
		if sp := prof.sub("speciality"); sp != nil {
			sp.take("name", &out.Speciality)
		}
		prof.drop("picture_url")
		prof.drop("name_with_title_and_determiner")
	}
	out.Places = takeEach(data, "places", organisePlace)
	out.Practitioners = takeEach(data, "practitioners", organiseProfilePractitioner)
	names := practitionerNames(out.Practitioners)
	agendas := takeEach(data, "agendas", func(raw json.RawMessage) agenda { return readAgenda(raw, names) })
	judged := len(agendas) > 0 && !slices.ContainsFunc(agendas, func(a agenda) bool { return a.unread() })
	if judged {
		out.Practitioners = onAgendas(out.Practitioners, agendas)
	}
	// Categories are only peeked: they stay whole in other.
	ctx := motiveContext{agendas: agendas, listing: agendaListing(agendas), categories: categoryNames(data)}
	out.Motives = takeEach(data, "visit_motives", func(raw json.RawMessage) Motive { return organiseMotive(raw, ctx) })
	// What offered says leaves an agenda's other, which takes the motives.
	motives := motiveSet(out.Motives)
	for i := range agendas {
		agendas[i].finish(motives)
		out.Agendas = append(out.Agendas, agendas[i].out)
	}
	out.Other = data.rest()
	if data != top {
		out.Other = unwrap(out.Other, top.rest())
	}
	return &organisedProfile{out: out, agendas: agendas}, nil
}

// takeEach takes null as no items, as take does, and leaves any other field that isn't an array in place.
func takeEach[T any](o *object, key string, organise func(json.RawMessage) T) []T {
	out := []T{}
	var items []json.RawMessage
	if o.take(key, &items) {
		for _, item := range items {
			out = append(out, organise(item))
		}
	}
	return out
}

// unwrap keeps Doctolib's nesting when both levels have a field of the same name, so neither is lost.
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

// result is the whole profile if it fits in profileBudget, else the motive index; motiveIDs picks motives instead.
func (p *organisedProfile) result(motiveIDs []int64) (*ProfileOutput, error) {
	if len(motiveIDs) > 0 {
		return p.pick(motiveIDs)
	}
	n, err := jsonChars(p.out)
	if err != nil {
		return nil, err
	}
	if n <= profileBudget {
		return p.out, nil
	}
	return p.index(), nil
}

func (p *organisedProfile) index() *ProfileOutput {
	out := *p.out
	out.Practitioners, out.Agendas, out.Motives = []ProfilePractitioner{}, []ProfileAgenda{}, []Motive{}
	out.Other = map[string]any{}
	index := make([]MotiveSummary, 0, len(p.out.Motives))
	for _, m := range p.out.Motives {
		index = append(index, MotiveSummary{ID: m.ID, Name: m.Name, Category: m.Category, Telehealth: m.Telehealth})
	}
	out.MotiveIndex = &index
	return &out
}

// pick puts the motives in, in the order asked, until one doesn't fit; the first always goes in. Ids that repeat or
// aren't the profile's motives are ignored.
func (p *organisedProfile) pick(motiveIDs []int64) (*ProfileOutput, error) {
	motives := motiveSet(p.out.Motives)
	var wanted []int64
	for _, id := range motiveIDs {
		if motives[id] && !slices.Contains(wanted, id) {
			wanted = append(wanted, id)
		}
	}
	out := p.subset(nil)
	for n := range wanted {
		next := p.subset(wanted[:n+1])
		// The ids left out are in the result too.
		if n+1 < len(wanted) {
			next.RemainingMotiveIDs = wanted[n+1:]
		}
		if n > 0 {
			size, err := jsonChars(next)
			if err != nil {
				return nil, err
			}
			if size > profileBudget {
				return out, nil
			}
		}
		out = next
	}
	return out, nil
}

// subset has the motives of ids in their order, and the agendas that list them, bookable or not, with their
// practitioners, in Doctolib's order.
func (p *organisedProfile) subset(ids []int64) *ProfileOutput {
	out := *p.out
	out.Motives = []Motive{}
	for _, id := range ids {
		for _, m := range p.out.Motives {
			if m.ID != nil && *m.ID == id {
				out.Motives = append(out.Motives, m)
			}
		}
	}
	out.Agendas = []ProfileAgenda{}
	practitioners := map[int64]bool{}
	for i := range p.agendas {
		a := &p.agendas[i]
		if !slices.ContainsFunc(ids, a.lists) {
			continue
		}
		out.Agendas = append(out.Agendas, a.out)
		if a.out.PractitionerID != nil {
			practitioners[*a.out.PractitionerID] = true
		}
	}
	out.Practitioners = []ProfilePractitioner{}
	for _, pr := range p.out.Practitioners {
		if pr.ID != nil && practitioners[*pr.ID] {
			out.Practitioners = append(out.Practitioners, pr)
		}
	}
	return &out
}

// jsonChars counts what a client gets: the SDK's text result is no longer than this encoding.
func jsonChars(out *ProfileOutput) (int, error) {
	data, err := json.Marshal(out)
	if err != nil {
		return 0, fmt.Errorf("encode profile result: %w", err)
	}
	return utf8.RuneCount(data), nil
}

func motiveSet(motives []Motive) map[int64]bool {
	ids := map[int64]bool{}
	for _, m := range motives {
		if m.ID != nil {
			ids[*m.ID] = true
		}
	}
	return ids
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

// categoryNames leaves out a category whose id or name doesn't read, and only that one.
func categoryNames(data *object) map[int64]string {
	var categories []json.RawMessage
	data.peek("visit_motive_categories", &categories)
	names := map[int64]string{}
	for _, raw := range categories {
		var c struct {
			ID   *int64  `json:"id"`
			Name *string `json:"name"`
		}
		if json.Unmarshal(raw, &c) == nil && c.ID != nil && c.Name != nil {
			names[*c.ID] = *c.Name
		}
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
	o.dropEmpty()
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
	// name and name_with_title_and_determiner say what name_with_title does; cloudinary_public_id is a photo.
	o.drop("name")
	o.drop("name_with_title_and_determiner")
	o.drop("cloudinary_public_id")
	o.dropEmpty()
	p.Other = o.rest()
	return p
}

// agenda is an agenda being organised: what is left in its other depends on the motives.
type agenda struct {
	out ProfileAgenda
	obj *object
	// byPractice is visit_motive_ids_by_practice_id, nil when it doesn't read as a map.
	byPractice map[string][]int64
	// namesPractitioner is false when practitioner_id is missing or doesn't read; null names none.
	namesPractitioner bool
}

// unread agendas leave the profile's practitioners and restriction ids as they are: without an id or a
// practitioner_id, an agenda could be the one they name.
func (a *agenda) unread() bool {
	return a.out.AgendaID == nil || !a.namesPractitioner
}

func readAgenda(raw json.RawMessage, names map[int64]*string) agenda {
	var a agenda
	a.obj = parseObject(raw)
	if a.obj == nil {
		a.out.Other = notObject(raw)
		return a
	}
	a.obj.take("id", &a.out.AgendaID)
	a.obj.take("practice_id", &a.out.PracticeID)
	a.namesPractitioner = a.obj.take("practitioner_id", &a.out.PractitionerID)
	a.obj.take("booking_temporary_disabled", &a.out.BookingTemporaryDisabled)
	if a.out.PractitionerID != nil {
		a.out.Practitioner = names[*a.out.PractitionerID]
	}
	var byPractice map[string][]int64
	if a.obj.peek("visit_motive_ids_by_practice_id", &byPractice) && byPractice != nil {
		a.byPractice = byPractice
	}
	return a
}

// bookable agendas are the ones in offered: a null booking_temporary_disabled doesn't stop them.
func (a *agenda) bookable() bool {
	return a.out.AgendaID != nil && (a.out.BookingTemporaryDisabled == nil || !*a.out.BookingTemporaryDisabled)
}

func (a *agenda) lists(motive int64) bool {
	for _, motives := range a.byPractice {
		if slices.Contains(motives, motive) {
			return true
		}
	}
	return false
}

// listed is nil when the agenda's motives aren't known.
func (a *agenda) listed() map[int64]bool {
	if a.byPractice == nil {
		return nil
	}
	set := map[int64]bool{}
	for _, motives := range a.byPractice {
		for _, m := range motives {
			set[m] = true
		}
	}
	return set
}

// finish leaves the motive lists out of other when offered says all they say, and the fields of no use to an agent.
func (a *agenda) finish(motives map[int64]bool) {
	if a.obj == nil {
		return
	}
	if a.expressed(motives) {
		a.obj.drop("visit_motive_ids_by_practice_id")
		var ids []int64
		if a.obj.peek("visit_motive_ids", &ids) && maps.Equal(setOf(ids), a.listed()) {
			a.obj.drop("visit_motive_ids")
		}
	}
	a.obj.drop("organization_id")
	a.obj.drop("patient_base_id")
	a.obj.dropEmpty()
	a.out.Other = a.obj.rest()
}

// expressed is false for a disabled agenda, which is in no offered, and for a map naming a practice that isn't a
// number or a motive the profile doesn't have.
func (a *agenda) expressed(motives map[int64]bool) bool {
	if !a.bookable() || a.byPractice == nil {
		return false
	}
	for key, ids := range a.byPractice {
		if _, err := strconv.ParseInt(key, 10, 64); err != nil {
			return false
		}
		for _, id := range ids {
			if !motives[id] {
				return false
			}
		}
	}
	return true
}

func setOf(ids []int64) map[int64]bool {
	set := make(map[int64]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

// agendaListing maps each agenda id to the motives it lists, nil when they aren't known for every agenda with that
// id.
func agendaListing(agendas []agenda) map[int64]map[int64]bool {
	listing := map[int64]map[int64]bool{}
	for i := range agendas {
		a := &agendas[i]
		if a.out.AgendaID == nil {
			continue
		}
		id := *a.out.AgendaID
		listed, seen := listing[id]
		switch {
		case !seen:
			listing[id] = a.listed()
		case listed != nil && a.byPractice != nil:
			maps.Copy(listed, a.listed())
		default:
			listing[id] = nil
		}
	}
	return listing
}

type motiveContext struct {
	agendas []agenda
	// listing is agendaListing's.
	listing    map[int64]map[int64]bool
	categories map[int64]string
}

func organiseMotive(raw json.RawMessage, ctx motiveContext) Motive {
	m := Motive{Offered: []AvailabilityQuery{}}
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
	o.take("age_referral_restrictions", &m.AgeReferralRestrictions)
	o.drop("organization_id")
	o.dropEmpty()
	m.Other = o.rest()
	if m.ID == nil {
		return m
	}
	m.NewPatientRestrictions = ctx.narrow(m.NewPatientRestrictions, *m.ID)
	m.AgeReferralRestrictions = ctx.narrow(m.AgeReferralRestrictions, *m.ID)
	m.Offered = ctx.offered(*m.ID)
	return m
}

// offered has one query per practice, in practice order, with the bookable agendas that list the motive there in
// Doctolib's order.
func (c motiveContext) offered(motive int64) []AvailabilityQuery {
	byPractice := map[int64][]int64{}
	for i := range c.agendas {
		a := &c.agendas[i]
		if !a.bookable() {
			continue
		}
		for _, practice := range practicesOffering(a.byPractice, motive) {
			byPractice[practice] = append(byPractice[practice], *a.out.AgendaID)
		}
	}
	out := make([]AvailabilityQuery, 0, len(byPractice))
	for _, practice := range slices.Sorted(maps.Keys(byPractice)) {
		out = append(
			out,
			AvailabilityQuery{VisitMotiveID: motive, AgendaIDs: byPractice[practice], PracticeID: practice},
		)
	}
	return out
}

func practicesOffering(byPractice map[string][]int64, motive int64) []int64 {
	var out []int64
	for key, motives := range byPractice {
		practice, err := strconv.ParseInt(key, 10, 64)
		if err == nil && slices.Contains(motives, motive) {
			out = append(out, practice)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// narrow takes out of a restriction list the agendas known not to list the motive, and the entries with none
// left: they say nothing about it. Anything but an array stays as it is, and so does an entry without agenda_ids.
func (c motiveContext) narrow(raw json.RawMessage, motive int64) json.RawMessage {
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || entries == nil {
		return raw
	}
	kept := make([]json.RawMessage, 0, len(entries))
	for _, e := range entries {
		if entry, ok := c.narrowEntry(e, motive); ok {
			kept = append(kept, entry)
		}
	}
	data, err := json.Marshal(kept)
	if err != nil {
		return raw
	}
	return data
}

// narrowEntry is false for an entry with no agenda left. An agenda id the profile doesn't have stays.
func (c motiveContext) narrowEntry(raw json.RawMessage, motive int64) (json.RawMessage, bool) {
	o := parseObject(raw)
	var ids []int64
	if o == nil || !o.peek("agenda_ids", &ids) || len(ids) == 0 {
		return raw, true
	}
	left := slices.DeleteFunc(slices.Clone(ids), func(id int64) bool {
		listed, ok := c.listing[id]
		return ok && listed != nil && !listed[motive]
	})
	if len(left) == 0 {
		return nil, false
	}
	if len(left) == len(ids) {
		return raw, true
	}
	data, err := json.Marshal(left)
	if err != nil {
		return raw, true
	}
	o.fields["agenda_ids"] = data
	data, err = json.Marshal(o.fields)
	if err != nil {
		return raw, true
	}
	return data, true
}

// onAgendas keeps the practitioners an agenda names, and those whose id doesn't read.
func onAgendas(practitioners []ProfilePractitioner, agendas []agenda) []ProfilePractitioner {
	named := map[int64]bool{}
	for i := range agendas {
		if id := agendas[i].out.PractitionerID; id != nil {
			named[*id] = true
		}
	}
	return slices.DeleteFunc(practitioners, func(p ProfilePractitioner) bool { return p.ID != nil && !named[*p.ID] })
}

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tio-plato/doctolib-mcp/internal/place"
)

const (
	searchPath = "/patient-health-search/api/v1/hcp/search"
	pageSize   = 20
	tenths     = 10
)

type SearchInput struct {
	Query     string   `json:"query"               jsonschema:"speciality, keyword or practitioner name, e.g. dentiste"`
	Place     string   `json:"place"               jsonschema:"5-digit French postcode or commune name. In Paris, Lyon and Marseille a postcode means its arrondissement, the name the whole city"`
	Languages []string `json:"languages,omitempty" jsonschema:"ISO 639 codes, e.g. en or es; keeps practitioners who speak any of them"`
	Page      int      `json:"page,omitempty"      jsonschema:"page number, from 0; 20 results a page"`
	// The filters below passed a live check: Doctolib applied each one.
	Telehealth          *bool    `json:"telehealth,omitempty"            jsonschema:"only practitioners who offer video consultations"`
	Sectors             []string `json:"sectors,omitempty"               jsonschema:"regulation sectors to keep, as in the results' sector.code. Doctors use contracted_1*, contracted_2* and non_contracted; dentists and similar contracted and contracted_with_extra; organisations organization_*"`
	AvailableWithinDays *int     `json:"available_within_days,omitempty" jsonschema:"only practitioners with a free slot within this many days: 1 (today), 3, 7 or 14"`
}

type SearchOutput struct {
	Place place.Place `json:"place"`
	// SearchedLocation is Doctolib's search point, else the centre of the place's box.
	SearchedLocation place.Point    `json:"searched_location"`
	Total            *int           `json:"total"`
	Page             int            `json:"page"`
	PageSize         int            `json:"page_size"`
	HasMore          bool           `json:"has_more"`
	FilterWarnings   []string       `json:"filter_warnings"`
	Results          []Practitioner `json:"results"`
	Other            map[string]any `json:"other"`
}

type Practitioner struct {
	Name              string             `json:"name"`
	Kind              string             `json:"kind"`
	Title             *string            `json:"title"`
	FirstName         *string            `json:"first_name"`
	LastName          *string            `json:"last_name"`
	Speciality        *string            `json:"speciality"`
	SpecialitySlug    *string            `json:"speciality_slug"`
	Address           *string            `json:"address"`
	Zipcode           *string            `json:"zipcode"`
	City              *string            `json:"city"`
	Lat               *float64           `json:"lat"`
	Lng               *float64           `json:"lng"`
	DistanceKm        *float64           `json:"distance_km"`
	InArea            bool               `json:"in_area"`
	Languages         []Language         `json:"languages"`
	Sector            *Label             `json:"sector"`
	Telehealth        *bool              `json:"telehealth"`
	BookableOnline    bool               `json:"bookable_online"`
	AllowNewPatients  *bool              `json:"allow_new_patients"`
	PaymentMeans      json.RawMessage    `json:"payment_means"`
	MinimumFee        json.RawMessage    `json:"minimum_fee"`
	MatchedMotive     *MatchedMotive     `json:"matched_motive"`
	AvailabilityQuery *AvailabilityQuery `json:"availability_query"`
	ProfileURL        *string            `json:"profile_url"`
	Other             map[string]any     `json:"other"`
}

type MatchedMotive struct {
	ID   *int64  `json:"id"`
	Name *string `json:"name"`
}

type Label struct {
	Code  string  `json:"code"`
	Label *string `json:"label"`
}

type AvailabilityQuery struct {
	VisitMotiveID int64   `json:"visit_motive_id"`
	AgendaIDs     []int64 `json:"agenda_ids"`
	PracticeID    int64   `json:"practice_id"`
}

func (t *Tools) Search(ctx context.Context, in SearchInput) (*SearchOutput, error) {
	codes, unknown, err := doctolibLanguages(in.Languages)
	if err != nil {
		return nil, err
	}
	in.Languages = codes
	plc, err := t.places.Resolve(ctx, in.Place)
	if err != nil {
		return nil, fmt.Errorf("resolve place: %w", err)
	}
	raw, err := t.doctolib.Post(ctx, searchPath, "page="+strconv.Itoa(in.Page), searchBody(in, plc, t.today()))
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	out, err := organiseSearch(raw, plc, in.Page, t.doctolib.BaseURL())
	if err != nil {
		return nil, err
	}
	out.FilterWarnings = filterWarnings(in, unknown, out.Total, out.Results)
	return out, nil
}

type latLng struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type searchRequest struct {
	Keyword  string         `json:"keyword"`
	Location searchLocation `json:"location"`
	Filters  map[string]any `json:"filters"`
}

type searchLocation struct {
	Place searchPlace `json:"place"`
}

type searchPlace struct {
	Name     string   `json:"name"`
	Slug     string   `json:"slug"`
	Country  string   `json:"country"`
	Type     string   `json:"type"`
	Viewport viewport `json:"viewport"`
	GPSPoint latLng   `json:"gpsPoint"`
}

type viewport struct {
	Northeast latLng `json:"northeast"`
	Southwest latLng `json:"southwest"`
}

// searchBody carries the full place and a filters object: without them Doctolib answers 422.
func searchBody(in SearchInput, plc place.Place, today time.Time) searchRequest {
	filters := map[string]any{}
	if len(in.Languages) > 0 {
		filters["languages"] = in.Languages
	}
	if in.Telehealth != nil && *in.Telehealth {
		filters["telehealth"] = true
	}
	if len(in.Sectors) > 0 {
		upper := make([]string, 0, len(in.Sectors))
		for _, s := range in.Sectors {
			upper = append(upper, strings.ToUpper(s))
		}
		filters["regulationSector"] = upper
	}
	if in.AvailableWithinDays != nil && *in.AvailableWithinDays > 0 {
		// As the web front end sends it: the end of the last day, Paris time, in UTC.
		end := today.AddDate(0, 0, *in.AvailableWithinDays)
		filters["availabilitiesBefore"] = end.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	return searchRequest{
		Keyword: in.Query,
		Location: searchLocation{Place: searchPlace{
			Name:    plc.Name,
			Slug:    place.Slug(plc.Name),
			Country: "fr",
			Type:    "locality",
			Viewport: viewport{
				Northeast: latLng{Lat: plc.BBox.North, Lng: plc.BBox.East},
				Southwest: latLng{Lat: plc.BBox.South, Lng: plc.BBox.West},
			},
			GPSPoint: latLng{Lat: plc.Centre.Lat, Lng: plc.Centre.Lng},
		}},
		Filters: filters,
	}
}

var errNotObject = errors.New("not a JSON object")

func organiseSearch(raw []byte, plc place.Place, page int, base string) (*SearchOutput, error) {
	top := parseObject(raw)
	if top == nil {
		return nil, fmt.Errorf("decode search response: %w", errNotObject)
	}
	out := &SearchOutput{
		Place: plc, SearchedLocation: plc.BBox.Centre(), Total: nil, Page: page, PageSize: pageSize,
		HasMore: false, FilterWarnings: []string{}, Results: []Practitioner{}, Other: nil,
	}
	if loc := top.sub("searchedLocation"); loc != nil {
		var lat, lng *float64
		if loc.peek("lat", &lat) && loc.peek("lng", &lng) && lat != nil && lng != nil {
			loc.drop("lat")
			loc.drop("lng")
			out.SearchedLocation = place.Point{Lat: *lat, Lng: *lng}
		}
	}
	top.take("total", &out.Total)
	out.HasMore = out.Total != nil && (page+1)*pageSize < *out.Total

	var items []json.RawMessage
	if top.take("healthcareProviders", &items) {
		for _, item := range items {
			out.Results = append(out.Results, organisePractitioner(item, out.SearchedLocation, plc, base))
		}
	}
	out.Other = top.rest()
	return out, nil
}

func organisePractitioner(raw json.RawMessage, ref place.Point, plc place.Place, base string) Practitioner {
	var p Practitioner
	o := parseObject(raw)
	if o == nil {
		p.Other = notObject(raw)
		return p
	}
	organiseIdentity(o, &p)
	organiseLocation(o, &p, ref, plc)
	organiseBooking(o, &p)

	var codes []string
	if o.take("languages", &codes) && codes != nil {
		p.Languages = make([]Language, 0, len(codes))
		for _, c := range codes {
			p.Languages = append(p.Languages, languageOf(c))
		}
	}
	var sector *string
	if o.take("regulationSector", &sector) && sector != nil {
		l := label(sectorLabels(), *sector)
		p.Sector = &l
	}
	o.take("paymentMeans", &p.PaymentMeans)
	o.take("minimumFee", &p.MinimumFee)
	var link *string
	if o.take("link", &link) && link != nil {
		u := base + *link
		p.ProfileURL = &u
	}
	p.Other = o.rest()
	return p
}

func organiseIdentity(o *object, p *Practitioner) {
	var typ *string
	o.peek("type", &typ) // kind is derived; type itself stays in other
	switch {
	case typ == nil:
		p.Kind = ""
	case *typ == "INDIVIDUAL_PRACTITIONER":
		p.Kind = "practitioner"
	case *typ == "ORGANIZATION":
		p.Kind = "organization"
	default:
		p.Kind = *typ
	}
	o.take("title", &p.Title)
	o.take("firstName", &p.FirstName)
	var name *string
	if o.take("name", &name) && p.Kind == "organization" {
		p.Name = deref(name)
	} else {
		p.LastName = name
		p.Name = strings.Join(strings.Fields(deref(p.Title)+" "+deref(p.FirstName)+" "+deref(name)), " ")
	}
	if sp := o.sub("speciality"); sp != nil {
		sp.take("name", &p.Speciality)
		sp.take("slug", &p.SpecialitySlug)
	}
}

func organiseLocation(o *object, p *Practitioner, ref place.Point, plc place.Place) {
	loc := o.sub("location")
	if loc == nil {
		return
	}
	loc.take("address", &p.Address)
	loc.take("zipcode", &p.Zipcode)
	loc.take("city", &p.City)
	loc.take("lat", &p.Lat)
	loc.take("lng", &p.Lng)
	if p.Lat != nil && p.Lng != nil {
		d := math.Round(place.Distance(ref, place.Point{Lat: *p.Lat, Lng: *p.Lng})*tenths) / tenths
		p.DistanceKm = &d
	}
	p.InArea = p.Zipcode != nil && slices.Contains(plc.Postcodes, *p.Zipcode)
}

func organiseBooking(o *object, p *Practitioner) {
	online := o.sub("onlineBooking")
	matched := o.sub("matchedVisitMotive")
	refs := o.sub("references")
	p.BookableOnline = online != nil
	if online != nil {
		online.take("telehealth", &p.Telehealth)
	}
	var motiveID *int64
	if matched != nil {
		p.MatchedMotive = &MatchedMotive{ID: nil, Name: nil}
		matched.take("visitMotiveId", &p.MatchedMotive.ID)
		matched.take("name", &p.MatchedMotive.Name)
		matched.take("allowNewPatients", &p.AllowNewPatients)
		motiveID = p.MatchedMotive.ID
	}
	var practiceID *int64
	if refs == nil || !refs.peek("practiceId", &practiceID) || practiceID == nil || motiveID == nil || online == nil {
		return
	}
	// The agendas come from the matched motive (not nil: motiveID came from it), else from onlineBooking, which lists
	// every agenda of the speciality there: the same slots from a longer list.
	var agendas []int64
	from := matched
	if !matched.peek("agendaIds", &agendas) || len(agendas) == 0 {
		from = online
		if !online.peek("agendaIds", &agendas) || len(agendas) == 0 {
			return
		}
	}
	from.drop("agendaIds")
	refs.drop("practiceId")
	p.AvailabilityQuery = &AvailabilityQuery{VisitMotiveID: *motiveID, AgendaIDs: agendas, PracticeID: *practiceID}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

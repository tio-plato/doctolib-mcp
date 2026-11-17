// Package place turns a French postcode or commune name into the area a
// Doctolib search needs, using geo.api.gouv.fr: only the postcode or the name
// leaves the machine.
package place

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the French government's geographic API.
const DefaultBaseURL = "https://geo.api.gouv.fr"

const (
	earthRadiusKm  = 6371
	nameLimit      = 20
	maxCandidates  = 5
	requestTimeout = 15 * time.Second
	maxBody        = 8 << 20
	fields         = "nom,code,centre,bbox,codesPostaux"
	degToRad       = math.Pi / 180
	// A GeoJSON position is [longitude, latitude].
	positionLen = 2
)

var postcodePattern = regexp.MustCompile(`^[0-9]{5}$`)

// Point is a WGS84 position.
type Point struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// BBox is a bounding box.
type BBox struct {
	South float64 `json:"south"`
	West  float64 `json:"west"`
	North float64 `json:"north"`
	East  float64 `json:"east"`
}

// Centre is the middle of the box.
func (b BBox) Centre() Point {
	return Point{Lat: (b.South + b.North) / 2, Lng: (b.West + b.East) / 2} //nolint:mnd // the middle
}

// Place is a resolved arrondissement or commune.
type Place struct {
	Name      string   `json:"name"`
	INSEE     string   `json:"insee"`
	Postcodes []string `json:"postcodes"`
	BBox      BBox     `json:"bbox"`
	Centre    Point    `json:"centre"`
}

// Candidate is a commune the input may have meant.
type Candidate struct {
	Name      string
	INSEE     string
	Postcodes []string
}

// Error reports a postcode or name that names no single place.
type Error struct {
	Input      string
	Reason     string
	Candidates []Candidate
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%q: %s", e.Input, e.Reason)
	for i, c := range e.Candidates {
		sep := "; "
		if i == 0 {
			sep = ": "
		}
		fmt.Fprintf(&b, "%s%s (INSEE %s, postcodes %s)", sep, c.Name, c.INSEE, strings.Join(c.Postcodes, ", "))
	}
	return b.String()
}

// Resolver queries geo.api.gouv.fr.
type Resolver struct {
	baseURL string
	http    *http.Client
}

// NewResolver returns a resolver for the API at baseURL.
func NewResolver(baseURL string) *Resolver {
	return &Resolver{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// Resolve turns a 5-digit postcode or a commune name into a place.
//
// A postcode resolves to its arrondissement in Paris, Lyon and Marseille, else
// to the first commune that has it. A name resolves only to a commune whose
// name is the same, ignoring case, accents and hyphens; several such communes
// make an [Error] that lists their postcodes.
func (r *Resolver) Resolve(ctx context.Context, input string) (Place, error) {
	input = strings.TrimSpace(input)
	if postcodePattern.MatchString(input) {
		return r.byPostcode(ctx, input)
	}
	return r.byName(ctx, input)
}

// Distance is the great-circle distance in kilometres (haversine, Earth radius 6371 km).
func Distance(a, b Point) float64 {
	halfLat := math.Sin((b.Lat - a.Lat) * degToRad / 2) //nolint:mnd // haversine
	halfLng := math.Sin((b.Lng - a.Lng) * degToRad / 2) //nolint:mnd // haversine
	h := halfLat*halfLat + math.Cos(a.Lat*degToRad)*math.Cos(b.Lat*degToRad)*halfLng*halfLng
	return 2 * earthRadiusKm * math.Asin(math.Sqrt(h))
}

// Slug is name in lower case, without accents, with every run of other characters turned into one hyphen.
func Slug(name string) string {
	return strings.ReplaceAll(fold(name), " ", "-")
}

// fold lowercases s, removes accents and turns every run of non-alphanumeric characters into one space.
func fold(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		if f, ok := unaccent(r); ok {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(f)
			space = false
		} else {
			space = true
		}
	}
	return b.String()
}

// unaccent maps a lowercase letter or digit to its unaccented spelling; ok is false for anything else.
func unaccent(r rune) (string, bool) {
	switch {
	case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		return string(r), true
	case strings.ContainsRune("àâäáãå", r):
		return "a", true
	case r == 'ç':
		return "c", true
	case strings.ContainsRune("éèêë", r):
		return "e", true
	case strings.ContainsRune("îïíì", r):
		return "i", true
	case r == 'ñ':
		return "n", true
	case strings.ContainsRune("ôöóòõø", r):
		return "o", true
	case strings.ContainsRune("ùûüú", r):
		return "u", true
	case strings.ContainsRune("ÿý", r):
		return "y", true
	case r == 'œ':
		return "oe", true
	case r == 'æ':
		return "ae", true
	default:
		return "", false
	}
}

// commune is one geo.api.gouv.fr result.
type commune struct {
	Nom          string   `json:"nom"`
	Code         string   `json:"code"`
	CodesPostaux []string `json:"codesPostaux"`
	Centre       struct {
		Coordinates []float64 `json:"coordinates"`
	} `json:"centre"`
	BBox struct {
		Coordinates [][][]float64 `json:"coordinates"`
	} `json:"bbox"`
}

func (r *Resolver) byPostcode(ctx context.Context, postcode string) (Place, error) {
	found, err := r.communes(ctx, url.Values{"codePostal": {postcode}, "type": {"arrondissement-municipal"}})
	if err != nil {
		return Place{}, err
	}
	if len(found) == 0 {
		found, err = r.communes(ctx, url.Values{"codePostal": {postcode}})
		if err != nil {
			return Place{}, err
		}
	}
	if len(found) == 0 {
		return Place{}, &Error{Input: postcode, Reason: "no commune has this postcode", Candidates: nil}
	}
	// The box only orders Doctolib's results, so the first commune is enough.
	return found[0].place(postcode)
}

func (r *Resolver) byName(ctx context.Context, name string) (Place, error) {
	found, err := r.communes(ctx, url.Values{
		"nom": {name}, "boost": {"population"}, "limit": {strconv.Itoa(nameLimit)},
	})
	if err != nil {
		return Place{}, err
	}
	want := fold(name)
	var same []commune
	for _, c := range found {
		if fold(c.Nom) == want {
			same = append(same, c)
		}
	}
	switch len(same) {
	case 1:
		return same[0].place(name)
	case 0:
		if len(found) == 0 {
			return Place{}, &Error{Input: name, Reason: "no commune has a name like this", Candidates: nil}
		}
		closest := found[:min(len(found), maxCandidates)]
		return Place{}, &Error{
			Input: name, Reason: "no commune has exactly this name; the closest are", Candidates: candidates(closest),
		}
	default:
		return Place{}, &Error{
			Input: name, Reason: "several communes have this name; call again with a postcode of one of them",
			Candidates: candidates(same),
		}
	}
}

func (r *Resolver) communes(ctx context.Context, query url.Values) ([]commune, error) {
	query.Set("fields", fields)
	query.Set("format", "json")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/communes?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build geo.api.gouv.fr request: %w", err)
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query geo.api.gouv.fr: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query geo.api.gouv.fr: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("read geo.api.gouv.fr response: %w", err)
	}
	var found []commune
	err = json.Unmarshal(data, &found)
	if err != nil {
		return nil, fmt.Errorf("decode geo.api.gouv.fr response: %w", err)
	}
	return found, nil
}

var errNoGeometry = errors.New("geo.api.gouv.fr returned no centre or bounding box")

func (c commune) place(input string) (Place, error) {
	if len(c.Centre.Coordinates) != positionLen || len(c.BBox.Coordinates) == 0 || len(c.BBox.Coordinates[0]) == 0 {
		return Place{}, fmt.Errorf("resolve %q to %s: %w", input, c.Nom, errNoGeometry)
	}
	ring := c.BBox.Coordinates[0]
	box := BBox{South: math.Inf(1), West: math.Inf(1), North: math.Inf(-1), East: math.Inf(-1)}
	for _, p := range ring {
		if len(p) < positionLen {
			return Place{}, fmt.Errorf("resolve %q to %s: %w", input, c.Nom, errNoGeometry)
		}
		box.West, box.East = math.Min(box.West, p[0]), math.Max(box.East, p[0])
		box.South, box.North = math.Min(box.South, p[1]), math.Max(box.North, p[1])
	}
	postcodes := c.CodesPostaux
	if postcodes == nil {
		postcodes = []string{}
	}
	return Place{
		Name:      c.Nom,
		INSEE:     c.Code,
		Postcodes: postcodes,
		BBox:      box,
		Centre:    Point{Lat: c.Centre.Coordinates[1], Lng: c.Centre.Coordinates[0]},
	}, nil
}

func candidates(cs []commune) []Candidate {
	out := make([]Candidate, 0, len(cs))
	for _, c := range cs {
		out = append(out, Candidate{Name: c.Nom, INSEE: c.Code, Postcodes: c.CodesPostaux})
	}
	return out
}

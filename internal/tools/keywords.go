package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

const keywordsPath = "/patient-health-search/api/v1/autocomplete"

type KeywordsInput struct {
	Query string `json:"query" jsonschema:"words as typed in Doctolib's search box: a speciality, an act or condition, a kind of centre or a name, e.g. détartrage"`
}

type KeywordsOutput struct {
	Keywords []Keyword        `json:"keywords"`
	Profiles []KeywordProfile `json:"profiles"`
	Other    map[string]any   `json:"other"`
}

type Keyword struct {
	Slug  *string        `json:"slug"`
	Name  *string        `json:"name"`
	Kind  *string        `json:"kind"`
	Other map[string]any `json:"other"`
}

// KeywordProfile leaves the name, speciality and city in other: an agent reads them there as Doctolib gives them.
type KeywordProfile struct {
	ProfileURL *string        `json:"profile_url"`
	Other      map[string]any `json:"other"`
}

type keywordsRequest struct {
	Query string `json:"query"`
}

// Keywords sends the words as given: Doctolib folds case and accents itself.
func (t *Tools) Keywords(ctx context.Context, in KeywordsInput) (*KeywordsOutput, error) {
	raw, err := t.doctolib.Post(ctx, keywordsPath, "", keywordsRequest(in))
	if err != nil {
		return nil, fmt.Errorf("suggest keywords: %w", err)
	}
	return organiseKeywords(raw, t.doctolib.BaseURL())
}

func organiseKeywords(raw []byte, base string) (*KeywordsOutput, error) {
	top := parseObject(raw)
	if top == nil {
		return nil, fmt.Errorf("decode autocomplete response: %w", errNotObject)
	}
	out := &KeywordsOutput{Keywords: []Keyword{}, Profiles: []KeywordProfile{}, Other: nil}
	var entries, profiles []json.RawMessage
	if top.take("searchEntries", &entries) {
		for _, entry := range entries {
			out.Keywords = append(out.Keywords, organiseKeyword(entry))
		}
	}
	if top.take("profiles", &profiles) {
		for _, profile := range profiles {
			out.Profiles = append(out.Profiles, organiseKeywordProfile(profile, base))
		}
	}
	out.Other = top.rest()
	return out, nil
}

func organiseKeyword(raw json.RawMessage) Keyword {
	var k Keyword
	o := parseObject(raw)
	if o == nil {
		k.Other = notObject(raw)
		return k
	}
	o.take("slug", &k.Slug)
	o.take("name", &k.Name)
	o.take("kind", &k.Kind)
	k.Other = o.rest()
	return k
}

func organiseKeywordProfile(raw json.RawMessage, base string) KeywordProfile {
	var p KeywordProfile
	o := parseObject(raw)
	if o == nil {
		p.Other = notObject(raw)
		return p
	}
	p.ProfileURL = takeProfileURL(o, base)
	p.Other = o.rest()
	return p
}

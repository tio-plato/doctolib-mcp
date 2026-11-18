package tools

import (
	"fmt"
	"slices"
	"strings"
)

// filterWarnings names the requested filters that the results show Doctolib
// did not apply: Doctolib silently ignores a filter a speciality does not
// offer, such as video consultations for dentists. It applies or ignores a
// filter for the whole search, so one result that does not match is enough.
// A result whose field is null is counted apart, as one that cannot be checked.
func filterWarnings(in SearchInput, unknown []string, results []Practitioner) []string {
	warnings := []string{}
	if len(unknown) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"languages: the server does not know %s, sent to Doctolib as is; Doctolib may spell this language "+
				"another way, so practitioners who speak it may be missing", strings.Join(unknown, ", ")))
	}
	if in.Telehealth != nil && *in.Telehealth {
		warnings = appendCheck(warnings, results, "telehealth", "do not offer video consultations",
			func(p Practitioner) (bool, bool) {
				if p.Telehealth == nil {
					return false, false
				}
				return *p.Telehealth, true
			})
	}
	if len(in.Sectors) > 0 {
		warnings = appendCheck(warnings, results, "sectors", "are in another regulation sector",
			func(p Practitioner) (bool, bool) {
				if p.Sector == nil {
					return false, false
				}
				return slices.Contains(in.Sectors, p.Sector.Code), true
			})
	}
	if len(in.Languages) > 0 {
		warnings = appendCheck(warnings, results, "languages", "speak none of the requested languages",
			func(p Practitioner) (bool, bool) {
				if p.Languages == nil {
					return false, false
				}
				return slices.ContainsFunc(p.Languages, func(l Language) bool {
					return slices.Contains(in.Languages, l.Code)
				}), true
			})
	}
	return warnings
}

// appendCheck adds a warning when a result that can be checked does not match.
func appendCheck(
	warnings []string, results []Practitioner, filter, mismatch string, matches func(Practitioner) (bool, bool),
) []string {
	off, unchecked := 0, 0
	for _, p := range results {
		ok, known := matches(p)
		switch {
		case !known:
			unchecked++
		case !ok:
			off++
		}
	}
	if off == 0 {
		return warnings
	}
	w := fmt.Sprintf(
		"%s: %d of %d results %s, so Doctolib did not apply this filter",
		filter,
		off,
		len(results),
		mismatch,
	)
	if unchecked > 0 {
		w += fmt.Sprintf(" (%d could not be checked)", unchecked)
	}
	return append(warnings, w)
}

// Package tools serves the MCP tools and organises Doctolib's responses.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"
	_ "time/tzdata" // the Paris date must not depend on the host's zoneinfo

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tio-plato/doctolib-mcp/internal/doctolib"
	"github.com/tio-plato/doctolib-mcp/internal/gate"
	"github.com/tio-plato/doctolib-mcp/internal/place"
)

type Tools struct {
	doctolib *doctolib.Client
	places   *place.Resolver
	now      func() time.Time
	paris    *time.Location
}

func New(client *doctolib.Client, places *place.Resolver, now func() time.Time) (*Tools, error) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		return nil, fmt.Errorf("load the Europe/Paris time zone: %w", err)
	}
	return &Tools{doctolib: client, places: places, now: now, paris: paris}, nil
}

func NewServer(t *Tools, version string) (*mcp.Server, error) {
	server := mcp.NewServer(&mcp.Implementation{Name: "doctolib", Title: "Doctolib (France)", Version: version}, nil)

	searchIn, err := schemaFor[SearchInput]()
	if err != nil {
		return nil, err
	}
	searchIn.Properties["page"].Minimum = jsonschema.Ptr(0.0)
	languages := searchIn.Properties["languages"]
	languages.Items.Pattern = `^[a-z]{2,3}$`
	searchIn.Properties["sectors"].Items.Enum = toAny(sectorCodes())
	// Null, like an absent value, means no such filter.
	searchIn.Properties["available_within_days"].Enum = []any{nil, 1, 3, 7, 14}
	searchOut, err := schemaFor[SearchOutput]()
	if err != nil {
		return nil, err
	}
	mcp.AddTool(server, tool("search_practitioners", searchDescription, searchIn, searchOut), handler(t.Search))

	profileIn, err := schemaFor[ProfileInput]()
	if err != nil {
		return nil, err
	}
	profileOut, err := schemaFor[ProfileOutput]()
	if err != nil {
		return nil, err
	}
	mcp.AddTool(server, tool("practitioner_profile", profileDescription, profileIn, profileOut), handler(t.Profile))

	availabilityIn, err := schemaFor[AvailabilityInput]()
	if err != nil {
		return nil, err
	}
	queries := availabilityIn.Properties["queries"]
	queries.MinItems, queries.MaxItems = jsonschema.Ptr(1), jsonschema.Ptr(MaxQueries)
	agendas := queries.Items.Properties["agenda_ids"]
	agendas.MinItems = jsonschema.Ptr(1)
	// Inferred slices also accept null, which would bypass the item counts.
	for _, s := range []*jsonschema.Schema{queries, agendas} {
		s.Type, s.Types = "array", nil
	}
	availabilityIn.Properties["start_date"].Pattern = `^[0-9]{4}-[0-9]{2}-[0-9]{2}$`
	availabilityOut, err := schemaFor[AvailabilityOutput]()
	if err != nil {
		return nil, err
	}
	mcp.AddTool(server, tool("availability", availabilityDescription, availabilityIn, availabilityOut),
		handler(t.Availability))
	return server, nil
}

// schemaFor maps [json.RawMessage] to the empty schema: inferred as a byte array, it would fail Doctolib's values
// in output validation.
func schemaFor[T any]() (*jsonschema.Schema, error) {
	s, err := jsonschema.For[T](&jsonschema.ForOptions{
		IgnoreInvalidTypes: false,
		TypeSchemas:        map[reflect.Type]*jsonschema.Schema{reflect.TypeFor[json.RawMessage](): {}},
	})
	if err != nil {
		return nil, fmt.Errorf("infer schema: %w", err)
	}
	return s, nil
}

func tool(name, description string, in, out *jsonschema.Schema) *mcp.Tool {
	return &mcp.Tool{
		Name:         name,
		Description:  description,
		InputSchema:  in,
		OutputSchema: out,
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: true},
	}
}

func handler[In, Out any](f func(context.Context, In) (*Out, error)) mcp.ToolHandlerFor[In, *Out] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, *Out, error) {
		out, err := f(ctx, in)
		if err != nil {
			return nil, nil, errors.New(ErrorText(err))
		}
		return nil, out, nil
	}
}

func ErrorText(err error) string {
	var (
		blocked *gate.BlockedError
		budget  *gate.BudgetError
		plc     *place.Error
		partial *partialError
		text    string
	)
	switch {
	case errors.As(err, &blocked):
		text = fmt.Sprintf("blocked: Doctolib blocked a request (HTTP %d on %s at %s). Page text: %q. "+
			"Stop now: tell the user this message as it is; do not retry, and do not reach Doctolib "+
			"through a browser or any other way. Every Doctolib tool stays off until the marker file %s "+
			"is deleted; delete it only after the user agrees.",
			blocked.Marker.Status, blocked.Marker.Path, blocked.Marker.Time.Format(time.RFC3339),
			blocked.Marker.Text, blocked.MarkerPath)
		if blocked.WriteErr != nil {
			text += fmt.Sprintf(" Writing the marker failed (%v): stop anyway.", blocked.WriteErr)
		}
	case errors.As(err, &budget):
		text = fmt.Sprintf("budget: the cap of %d Doctolib requests per %s is reached; requests resume at %s. "+
			"Tell the user; do not try another way.", budget.Limit, budget.Window, budget.Resume.Format(time.RFC3339))
	case errors.As(err, &plc):
		text = "place: " + plc.Error()
	default:
		text = "error: " + err.Error() + ". Tell the user; do not retry with the same arguments."
	}
	if errors.As(err, &partial) && len(partial.done.Results) > 0 {
		done, merr := json.Marshal(partial.done)
		if merr != nil {
			text += fmt.Sprintf(" The results fetched before it stopped could not be encoded: %v.", merr)
		} else {
			text += " Results fetched before it stopped, not to be fetched again: " + string(done)
		}
	}
	return text
}

func toAny(ss []string) []any {
	out := make([]any, 0, len(ss))
	for _, s := range ss {
		out = append(out, s)
	}
	return out
}

const blockedRule = `If a tool error starts with "blocked": stop, show the user the error as it is, ` +
	"and don't retry or reach Doctolib any other way, browser included. " +
	`If it starts with "budget": tell the user when requests resume and don't try another way.`

const searchDescription = `Doctolib (France): search practitioners near a French postcode or town.
Read-only, doctolib.fr only. It never books: give the user a result's profile_url to book there.
Results are sorted by distance and can lie outside the area. distance_km is the straight-line distance from searched_location; in_area is true when the result's postcode is one of place.postcodes.
Doctolib silently ignores a filter the speciality doesn't offer, such as telehealth for dentists, so read filter_warnings before trusting a filtered page. available_within_days can't be checked from the results; the availability tool shows the actual slots.
Pass a result's availability_query to availability and its profile_url to practitioner_profile. availability_query is null when the result can't be booked online (bookable_online false).
allow_new_patients is Doctolib's flag on matched_motive. false probably means that motive takes no new patients (inferred, not verified). It isn't a verdict: check practitioner_profile and availability.
other holds the Doctolib fields the server doesn't map, under their original names.
` + blockedRule

const profileDescription = `Doctolib (France): visit motives, places and agendas of one practitioner.
Read-only, doctolib.fr only; one Doctolib request.
Each motive's offered lists every (practice, agenda) that offers it, with the agenda's practitioner and an availability_query for the availability tool.
New patients: the server draws no conclusion. A motive's new_patient_restrictions is Doctolib's raw value, and each offered entry gets the entries that name its agenda. An entry probably lists agendas that take only existing patients for that motive (inferred, not verified). Judge together with availability: some motives are for existing patients only, and some new-patient motives have no slots or only distant ones.
unavailable_agendas have booking temporarily disabled (inferred) and are not in offered.
places[].phone is the practice's landline, for practitioners who can't be booked online.
other holds the Doctolib fields the server doesn't map, under their original names.
` + blockedRule

const availabilityDescription = `Doctolib (France): free slots and earliest slot for up to 20 practitioners.
Read-only, doctolib.fr only. Sends one Doctolib request per query, in turn. Ask the user before checking more than one page of practitioners for one question. Booking is the user's: give them the profile_url.
Each result covers 15 days from start_date (window, both ends included). status is the first of these that applies:
- has_slots: free slots in the window;
- not_opened: no slot, and Doctolib says slots aren't open yet (reason not_opened_availability);
- next_slot_known: no slot in the window, but Doctolib gives the next one after it (if that can't be read, earliest is null and other.next_slot holds it);
- none_visible: no slot in the window and no next slot. No bookable slot can be seen, which doesn't mean fully booked: never say fully booked.
earliest is the first slot in the window (source window); when the window has no slot at all, Doctolib's next slot (source next_slot; time is null when Doctolib gives only a date); else null. slots lists the free times by day, in Doctolib's local time (Paris for mainland France), e.g. "Thu 2026-11-19 09:00-12:40/10 14:05; Fri 2026-11-20 08:15": days are separated by "; " and start with the weekday and date; HH:MM is one slot, and HH:MM-HH:MM/N is a slot every N minutes from the first time to the last, both included. substitution lists, per day, the times Doctolib marks for another practitioner: status substitute or collaborator (probably a stand-in or a colleague seeing those slots; inferred, not verified). other.availabilities keeps only what these don't show, under each day's date: a slot in an unknown shape (counted in slot_count, not listed) or carrying more than its time (such as the later steps of a multi-step booking), and other day fields with a value.
A failed query gets status error and an error text; the others go on. A block or a reached cap stops the call, and the error carries the results already fetched.
` + blockedRule

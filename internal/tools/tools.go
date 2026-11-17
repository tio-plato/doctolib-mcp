// Package tools holds the three read-only MCP tools and organises Doctolib's
// responses for the calling agent.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	_ "time/tzdata" // the Paris date must not depend on the host's zoneinfo

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tio-plato/doctolib-mcp/internal/doctolib"
	"github.com/tio-plato/doctolib-mcp/internal/gate"
	"github.com/tio-plato/doctolib-mcp/internal/place"
)

// Tools runs the tools against Doctolib and geo.api.gouv.fr.
type Tools struct {
	doctolib *doctolib.Client
	places   *place.Resolver
	now      func() time.Time
	paris    *time.Location
}

// New returns the tools; now is the clock that sets "today" in Europe/Paris.
func New(client *doctolib.Client, places *place.Resolver, now func() time.Time) (*Tools, error) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		return nil, fmt.Errorf("load the Europe/Paris time zone: %w", err)
	}
	return &Tools{doctolib: client, places: places, now: now, paris: paris}, nil
}

// NewServer returns an MCP server named doctolib with the three tools.
func NewServer(t *Tools, version string) (*mcp.Server, error) {
	server := mcp.NewServer(&mcp.Implementation{Name: "doctolib", Title: "Doctolib (France)", Version: version}, nil)

	searchIn, err := schemaFor[SearchInput]()
	if err != nil {
		return nil, err
	}
	searchIn.Properties["page"].Minimum = jsonschema.Ptr(0.0)
	languages := searchIn.Properties["languages"]
	languages.Items.Enum = toAny(languageCodes())
	searchOut, err := schemaFor[SearchOutput]()
	if err != nil {
		return nil, err
	}
	mcp.AddTool(server, tool("search_practitioners", searchDescription(), searchIn, searchOut), handler(t.Search))

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

// schemaFor infers the schema of T. [json.RawMessage] holds Doctolib's values
// as they are, so it accepts any value instead of the byte array it would be
// inferred as.
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

// handler adapts a tool function to the SDK; an error becomes a tool error
// whose text starts with its category.
func handler[In, Out any](f func(context.Context, In) (*Out, error)) mcp.ToolHandlerFor[In, *Out] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, *Out, error) {
		out, err := f(ctx, in)
		if err != nil {
			return nil, nil, errors.New(ErrorText(err))
		}
		return nil, out, nil
	}
}

// ErrorText is the text of a tool error: it starts with the category, blocked, budget, place or error, and says what to do.
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

const blockedRule = `If a tool error starts with "blocked": stop, tell the user the error as it is, ` +
	"do not retry, and do not reach Doctolib through a browser or any other way. " +
	`If it starts with "budget": tell the user when requests resume and do not try another way.`

func searchDescription() string {
	var codes strings.Builder
	names := languageNames()
	for i, c := range languageCodes() {
		if i > 0 {
			codes.WriteString(", ")
		}
		codes.WriteString(c + "=" + names[c])
	}
	languages := "Language codes are Doctolib's own, not ISO: " + codes.String() + "."
	return `Doctolib (France): search practitioners near a French postcode or town.
Read-only and anonymous, doctolib.fr only. It never books: give the user a result's profile_url, where they book themselves.
Doctolib orders results by distance and does not keep them inside the area. Each result has distance_km, the straight-line distance from searched_location (Doctolib's search point), and in_area, true when its postcode is one of place.postcodes; place is what the postcode or name resolved to.
Pages start at 0, 20 results each; has_more tells whether another page exists.
Pass a result's availability_query to the availability tool (up to 20 in one call, one page of results) and its profile_url to practitioner_profile. availability_query is null when the result cannot be booked online (bookable_online false).
allow_new_patients is Doctolib's flag on matched_motive, copied as it is: false is inferred to mean that motive does not take new patients. It is not a verdict; judge with practitioner_profile and availability.
sector is the regulation sector (secteur) and languages the languages spoken, as Doctolib codes; a label is null when Doctolib gives none.
` + languages + `
other holds every Doctolib field the server did not organise, under its original name.
` + blockedRule
}

const profileDescription = `Doctolib (France): visit motives, places and agendas of one practitioner.
Read-only and anonymous, doctolib.fr only; one Doctolib request.
Each motive lists in offered every (practice, agenda) that offers it, with the agenda's practitioner and an availability_query ready for the availability tool.
New patients: the server draws no conclusion. new_patient_restrictions on a motive is Doctolib's value as it is; each offered entry copies the entries that name its agenda. Inferred, not verified: an entry names agendas that take only existing patients for that motive. Some practitioners open a motive to existing patients only, or a new-patient motive shows no slot or only far ones: judge together with the availability results.
unavailable_agendas have booking temporarily disabled; they are not in offered.
places[].phone is the practice's landline, for practitioners who cannot be booked online.
other holds every Doctolib field the server did not organise, under its original name.
` + blockedRule

const availabilityDescription = `Doctolib (France): free slots and earliest slot for up to 20 practitioners.
Read-only and anonymous, doctolib.fr only. Takes 1 to 20 availability_query objects, one page of search results, and sends one Doctolib request for each, one after another. If a question needs more than one page of practitioners checked, ask the user first. Booking is the user's: give them the profile_url.
Each result covers 15 days from start_date (window, both ends included). status, first rule that matches:
- has_slots: free slots in the window;
- not_opened: no slot in the window and Doctolib says the slots are not opened yet (reason not_opened_availability);
- next_slot_known: no slot in the window, but Doctolib gives the next slot after it;
- none_visible: no slot in the window and no next slot. It means no bookable slot can be seen, not that the practitioner is fully booked: never say fully booked.
earliest is the first slot in the window (source window); when the window has no slot, Doctolib's next slot (source next_slot; time is null when Doctolib gives only a date); else null. slots lists the days with free times, in the local time Doctolib writes them in (Paris time for mainland France); other.availabilities keeps the full ISO times. A slot in a shape the server does not know counts in slot_count but is not listed.
A query that fails has status error and an error text; the others go on. A block or a reached cap stops the call, and the error carries the results already fetched.
` + blockedRule

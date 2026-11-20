package tools_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tio-plato/doctolib-mcp/internal/tools"
)

// connect goes through the SDK, so it validates the arguments and results against the tools' schemas.
func connect(t *testing.T, e *env) *mcp.ClientSession {
	t.Helper()
	server, err := tools.NewServer(e.tools, "test")
	if err != nil {
		t.Fatal(err)
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	cs, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// callTool fails the test on any error and returns the structured result as a client decodes it.
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s: tool error %q", name, res.Content[0].(*mcp.TextContent).Text)
	}
	var structured map[string]any
	data, _ := json.Marshal(res.StructuredContent)
	err = json.Unmarshal(data, &structured)
	if err != nil {
		t.Fatalf("%s: structured content %s", name, data)
	}
	return structured
}

func TestToolsThroughTheSDK(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	cs := connect(t, e)

	for _, tc := range []struct {
		name string
		args map[string]any
		// other is where an "other" object must be in the structured result.
		other func(map[string]any) any
	}{
		{"search_practitioners", map[string]any{"query": "dentiste", "place": "75001", "languages": []string{"en"},
			"telehealth": true, "sectors": []string{"contracted_2"}, "available_within_days": 7},
			func(m map[string]any) any { return m["results"].([]any)[0].(map[string]any)["other"] }},
		{"practitioner_profile", map[string]any{"profile_url": e.base + "/dentiste/paris/ada-example"},
			func(m map[string]any) any { return m["other"] }},
		{"availability", map[string]any{"queries": []any{map[string]any{
			"visit_motive_id": 3001, "agenda_ids": []int{1001}, "practice_id": 2001,
		}}}, func(m map[string]any) any { return m["results"].([]any)[0].(map[string]any)["other"] }},
	} {
		structured := callTool(t, cs, tc.name, tc.args)
		if other, ok := tc.other(structured).(map[string]any); !ok || len(other) == 0 {
			t.Errorf("%s: no other in %v", tc.name, structured)
		}
	}
}

func TestAvailabilitySchemaRefusesMoreThanOnePage(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	cs := connect(t, e)

	queries := make([]any, tools.MaxQueries+1)
	for i := range queries {
		queries[i] = map[string]any{"visit_motive_id": 3001, "agenda_ids": []int{1001}, "practice_id": 2001}
	}
	res, err := cs.CallTool(
		t.Context(),
		&mcp.CallToolParams{Name: "availability", Arguments: map[string]any{"queries": queries}},
	)
	if err == nil && !res.IsError {
		t.Fatal("21 queries were accepted")
	}
	if err == nil {
		text := res.Content[0].(*mcp.TextContent).Text
		if !strings.Contains(text, "validating") {
			t.Errorf("error %q is not a schema validation error", text)
		}
	}
	if n := len(e.fake.doctolibRequests()); n != 0 {
		t.Errorf("sent %d Doctolib requests", n)
	}
}

func TestSearchSchemaRefusesMalformedLanguageCodes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	cs := connect(t, e)

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "search_practitioners",
		Arguments: map[string]any{"query": "dentiste", "place": "75001", "languages": []string{"English"}},
	})
	if err == nil && !res.IsError {
		t.Fatal("language English was accepted")
	}
	if n := len(e.fake.doctolibRequests()); n != 0 {
		t.Errorf("sent %d Doctolib requests", n)
	}
}

func TestAvailabilitySchemaRefusesNullAgendas(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	cs := connect(t, e)

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "availability", Arguments: map[string]any{
		"queries": []any{map[string]any{"visit_motive_id": 3001, "agenda_ids": nil, "practice_id": 2001}},
	}})
	if err == nil && !res.IsError {
		t.Fatal("agenda_ids null was accepted")
	}
	if n := len(e.fake.doctolibRequests()); n != 0 {
		t.Errorf("sent %d Doctolib requests", n)
	}
}

func TestSearchAcceptsNullForOptionalFilters(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	cs := connect(t, e)

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "search_practitioners",
		Arguments: map[string]any{
			"query": "dentiste", "place": "75001", "telehealth": nil, "available_within_days": nil, "sectors": nil,
		},
	})
	if err != nil || res.IsError {
		t.Fatalf("null optional filters were refused: %v %+v", err, res)
	}
}

func TestSearchAcceptsNullForOptionalLanguages(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	cs := connect(t, e)

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "search_practitioners",
		Arguments: map[string]any{"query": "dentiste", "place": "75001", "languages": nil},
	})
	if err != nil || res.IsError {
		t.Fatalf("languages null was refused: %v %+v", err, res)
	}
}

func TestSearchSchemaRefusesUnknownFilterValues(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	cs := connect(t, e)

	for _, args := range []map[string]any{
		{"query": "dentiste", "place": "75001", "sectors": []string{"secteur_2"}},
		{"query": "dentiste", "place": "75001", "available_within_days": 5},
	} {
		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "search_practitioners", Arguments: args})
		if err == nil && !res.IsError {
			t.Errorf("%v was accepted", args)
		}
	}
	if n := len(e.fake.doctolibRequests()); n != 0 {
		t.Errorf("sent %d Doctolib requests", n)
	}
}

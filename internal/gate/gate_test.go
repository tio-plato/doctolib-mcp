package gate_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tio-plato/doctolib-mcp/internal/gate"
)

const (
	helperDirEnv   = "DOCTOLIB_GATE_HELPER_DIR"
	helperStartEnv = "DOCTOLIB_GATE_HELPER_START"
	interval       = 500 * time.Millisecond
)

// TestHelperProcess is not a test: run as a child process, it waits for the
// common start time and passes the gate once.
func TestHelperProcess(t *testing.T) {
	t.Parallel()
	dir := os.Getenv(helperDirEnv)
	if dir == "" {
		t.Skip("only runs as a child process of TestIntervalHoldsAcrossProcesses")
	}
	start, err := strconv.ParseInt(os.Getenv(helperStartEnv), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(time.Unix(0, start)))
	g := gate.New(
		gate.Config{Dir: dir, Interval: interval, Hourly: gate.DefaultHourly, Daily: gate.DefaultDaily, Now: nil},
	)
	err = g.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
}

// Two server processes share the state directory: the second one is let
// through only once the interval since the first one has passed. The gap is
// read from the request log the processes share, where the gate records
// when it let each request through.
func TestIntervalHoldsAcrossProcesses(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	start := time.Now().Add(time.Second).UnixNano()
	cmds := make([]*exec.Cmd, 2)
	outputs := make([]strings.Builder, 2)
	for i := range cmds {
		cmds[i] = exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestHelperProcess$", "-test.v")
		cmds[i].Env = append(os.Environ(), helperDirEnv+"="+dir, helperStartEnv+"="+strconv.FormatInt(start, 10))
		cmds[i].Stdout = &outputs[i]
		cmds[i].Stderr = &outputs[i]
		err := cmds[i].Start()
		if err != nil {
			t.Fatal(err)
		}
	}
	for i, cmd := range cmds {
		err := cmd.Wait()
		if err != nil {
			t.Fatalf("child %d: %v\n%s", i, err, outputs[i].String())
		}
	}

	data, err := os.ReadFile(filepath.Join(dir, "requests.json"))
	if err != nil {
		t.Fatalf("no shared request log: %v", err)
	}
	var sent []time.Time
	err = json.Unmarshal(data, &sent)
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 {
		t.Fatalf("the shared request log holds %d requests, want 2: %s", len(sent), data)
	}
	if gap := sent[1].Sub(sent[0]); gap < interval {
		t.Errorf("the two processes were let through %v apart, want at least %v", gap, interval)
	}
}

// After the clock steps back an hour, the next request waits one interval, not an hour.
func TestClockSteppingBackDoesNotStall(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	g := gate.New(gate.Config{
		Dir: t.TempDir(), Interval: 100 * time.Millisecond, Hourly: gate.DefaultHourly, Daily: gate.DefaultDaily,
		Now: func() time.Time { return now },
	})
	err := g.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(-time.Hour)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err = g.Acquire(ctx)
	if err != nil {
		t.Fatalf("second request after the clock stepped back: %v", err)
	}
}

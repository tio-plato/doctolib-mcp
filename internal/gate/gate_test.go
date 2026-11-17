package gate_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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
// common start time, passes the gate once and prints when it was let through.
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
	_, _ = fmt.Fprintf(os.Stdout, "passed=%d\n", time.Now().UnixNano())
}

// Two server processes share the state directory: the second one is let
// through only once the interval since the first one has passed.
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
		if err := cmds[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	passed := make([]time.Time, 2)
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("child %d: %v\n%s", i, err, outputs[i].String())
		}
		passed[i] = passedAt(t, outputs[i].String())
	}

	gap := passed[1].Sub(passed[0]).Abs()
	if gap < interval {
		t.Errorf("the two processes passed %v apart, want at least %v", gap, interval)
	}
}

func passedAt(t *testing.T, output string) time.Time {
	t.Helper()
	for line := range strings.Lines(output) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "passed="); ok {
			ns, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			return time.Unix(0, ns)
		}
	}
	t.Fatalf("no passed= line in %q", output)
	return time.Time{}
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

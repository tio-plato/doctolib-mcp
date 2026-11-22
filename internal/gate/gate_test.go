package gate_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	// How late a child may report passing; a gate that doesn't wait lets both through at once, far below this.
	scheduling = interval / 2
)

// TestHelperProcess is not a test: run as a child process, it passes the gate once at the shared start time
// and prints when.
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
		gate.Config{
			Dir: dir, Interval: interval, Hourly: gate.DefaultHourly, Daily: gate.DefaultDaily, Now: nil, Sleep: nil,
		},
	)
	err = g.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(os.Stdout, "passed=%d\n", time.Now().UnixNano())
}

// The gap is read from the shared request log and checked against when each child saw Acquire return, give or
// take scheduling.
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
	passed := make([]time.Time, len(cmds))
	for i, cmd := range cmds {
		err := cmd.Wait()
		if err != nil {
			t.Fatalf("child %d: %v\n%s", i, err, outputs[i].String())
		}
		passed[i] = passedAt(t, outputs[i].String())
	}
	if gap := passed[1].Sub(passed[0]).Abs(); gap < interval-scheduling {
		t.Errorf("the children saw Acquire return %v apart, want about %v", gap, interval)
	}

	sent := readLog(t, dir)
	if len(sent) != 2 {
		t.Fatalf("the shared request log holds %d requests, want 2: %v", len(sent), sent)
	}
	if gap := sent[1].Sub(sent[0]); gap < interval {
		t.Errorf("the two processes were let through %v apart, want at least %v", gap, interval)
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
		Now: func() time.Time { return now }, Sleep: nil,
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

func TestBlockWrittenWhileWaitingIsHonoured(t *testing.T) {
	t.Parallel()
	asleep, wake := make(chan struct{}), make(chan struct{})
	g := gate.New(gate.Config{
		Dir: t.TempDir(), Interval: interval, Hourly: gate.DefaultHourly, Daily: gate.DefaultDaily, Now: nil,
		Sleep: func(context.Context, time.Duration) error {
			close(asleep)
			<-wake
			return nil
		},
	})
	err := g.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	waiting := make(chan error, 1)
	go func() { waiting <- g.Acquire(t.Context()) }()
	<-asleep
	written := make(chan error, 1)
	go func() {
		_, blockErr := g.Block("/availabilities.json", 403, "blocked")
		written <- blockErr
	}()
	select {
	case err = <-written:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		close(wake)
		t.Fatal("Block waited for the request that is waiting out the interval")
	}
	close(wake)
	var blocked *gate.BlockedError
	err = <-waiting
	if !errors.As(err, &blocked) {
		t.Errorf("the waiting request got %v, want a block", err)
	}
}

func TestLogStaysInOrderAfterTheClockStepsBack(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	g := gate.New(gate.Config{
		Dir: dir, Interval: time.Millisecond, Hourly: gate.DefaultHourly, Daily: gate.DefaultDaily,
		Now: func() time.Time { return now }, Sleep: nil,
	})
	for _, step := range []time.Duration{0, -time.Hour} {
		now = now.Add(step)
		err := g.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
	}
	sent := readLog(t, dir)
	if len(sent) != 2 || sent[1].Before(sent[0]) {
		t.Errorf("request log %v is not in order", sent)
	}
}

// On Windows, a file another handle has open can't be replaced until that handle closes.
func TestAcquireWaitsOutAReaderOfTheLog(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	g := gate.New(gate.Config{
		Dir: dir, Interval: time.Millisecond, Hourly: gate.DefaultHourly, Daily: gate.DefaultDaily,
		Now: nil, Sleep: nil,
	})
	err := g.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(filepath.Join(dir, "requests.json"))
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	time.AfterFunc(200*time.Millisecond, func() {
		_ = reader.Close()
		close(closed)
	})
	err = g.Acquire(t.Context())
	<-closed
	if err != nil {
		t.Fatalf("request while another handle had the log open: %v", err)
	}
	if sent := readLog(t, dir); len(sent) != 2 {
		t.Errorf("the request log holds %d requests, want 2: %v", len(sent), sent)
	}
}

func TestFailedReplaceLeavesNoTemporaryFile(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("only Windows fails to replace a file another handle has open")
	}
	dir := t.TempDir()
	g := gate.New(gate.Config{
		Dir: dir, Interval: time.Millisecond, Hourly: gate.DefaultHourly, Daily: gate.DefaultDaily,
		Now: nil, Sleep: nil,
	})
	err := g.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(filepath.Join(dir, "requests.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	err = g.Acquire(t.Context())
	if err == nil || !strings.Contains(err.Error(), "replace ") {
		t.Fatalf("Acquire() = %v, want a replace error", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "requests.json.") {
			t.Errorf("the failed replace left %s behind", e.Name())
		}
	}
}

func TestBlockKeepsTheFirstMarker(t *testing.T) {
	t.Parallel()
	g := gate.New(gate.Config{
		Dir: t.TempDir(), Interval: interval, Hourly: gate.DefaultHourly, Daily: gate.DefaultDaily,
		Now: nil, Sleep: nil,
	})
	for _, text := range []string{"first", "second"} {
		_, err := g.Block("/availabilities.json", 403, text)
		if err != nil {
			t.Fatal(err)
		}
	}
	var blocked *gate.BlockedError
	if err := g.CheckMarker(); !errors.As(err, &blocked) {
		t.Fatalf("CheckMarker() = %v, want a block", err)
	}
	if blocked.Marker.Text != "first" {
		t.Errorf("the marker holds %q, want the first block's %q", blocked.Marker.Text, "first")
	}
}

func readLog(t *testing.T, dir string) []time.Time {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "requests.json"))
	if err != nil {
		t.Fatalf("no shared request log: %v", err)
	}
	var sent []time.Time
	err = json.Unmarshal(data, &sent)
	if err != nil {
		t.Fatal(err)
	}
	return sent
}

func TestDefaultDirTakesOnlyAnAbsoluteXDGStateHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// os.UserHomeDir reads USERPROFILE on Windows.
	t.Setenv("USERPROFILE", home)
	for _, tc := range []struct{ xdg, want string }{
		{filepath.Join(home, "xdg"), filepath.Join(home, "xdg", "doctolib-mcp")},
		{"state", filepath.Join(home, ".local", "state", "doctolib-mcp")},
		{"", filepath.Join(home, ".local", "state", "doctolib-mcp")},
	} {
		t.Setenv("XDG_STATE_HOME", tc.xdg)
		got, err := gate.DefaultDir()
		if err != nil || got != tc.want {
			t.Errorf("XDG_STATE_HOME=%q: DefaultDir() = %q, %v; want %q", tc.xdg, got, err, tc.want)
		}
	}
}

func TestDefaultDirRefusesARelativeHome(t *testing.T) {
	// Taken before USERPROFILE changes: Windows takes the temporary directory from it when TMP and TEMP are unset.
	xdg := t.TempDir()
	t.Setenv("HOME", "home")
	t.Setenv("USERPROFILE", "home")
	t.Setenv("XDG_STATE_HOME", "")
	got, err := gate.DefaultDir()
	if err == nil {
		t.Errorf("relative home: DefaultDir() = %q, nil; want an error", got)
	}
	// An absolute XDG_STATE_HOME doesn't need the home directory.
	t.Setenv("XDG_STATE_HOME", xdg)
	got, err = gate.DefaultDir()
	if want := filepath.Join(xdg, "doctolib-mcp"); err != nil || got != want {
		t.Errorf("relative home, XDG_STATE_HOME=%q: DefaultDir() = %q, %v; want %q", xdg, got, err, want)
	}
}

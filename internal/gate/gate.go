// Package gate rations the requests sent to Doctolib.
//
// Every server process of the user shares one state directory, guarded by a
// file lock: one machine has one public IP address, so it gets one request
// log, one spacing and one block marker, however many MCP clients run.
package gate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Defaults sized on heavy manual use of the Doctolib website: one results page
// fires about 20 slot requests, 30 pages an hour make 600.
const (
	DefaultInterval = time.Second
	DefaultHourly   = 600
	DefaultDaily    = 2000
)

const (
	markerFile = "blocked.json"
	logFile    = "requests.json"
	lockFile   = "lock"
	day        = 24 * time.Hour
)

// Config sets up a [Gate].
type Config struct {
	// Dir is the state directory shared by every server process.
	Dir string
	// Interval is the minimum time between two requests.
	Interval time.Duration
	// Hourly and Daily cap the requests over the last hour and the last 24 hours.
	Hourly int
	Daily  int
	// Now is the clock; nil means [time.Now].
	Now func() time.Time
	// Sleep waits out the interval; nil means a timer.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Gate lets a request through once the block marker, the caps and the spacing allow it.
type Gate struct {
	dir      string
	interval time.Duration
	hourly   int
	daily    int
	now      func() time.Time
	sleep    func(ctx context.Context, d time.Duration) error
}

// New returns a gate over the state directory c.Dir.
func New(c Config) *Gate {
	g := &Gate{dir: c.Dir, interval: c.Interval, hourly: c.Hourly, daily: c.Daily, now: c.Now, sleep: c.Sleep}
	if g.now == nil {
		g.now = time.Now
	}
	if g.sleep == nil {
		g.sleep = sleep
	}
	return g
}

// DefaultDir is $XDG_STATE_HOME/doctolib-mcp, or ~/.local/state/doctolib-mcp when it is unset.
func DefaultDir() (string, error) {
	if base := os.Getenv("XDG_STATE_HOME"); base != "" {
		return filepath.Join(base, "doctolib-mcp"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find the state directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "doctolib-mcp"), nil
}

// Marker records the response Doctolib blocked.
type Marker struct {
	Time   time.Time `json:"time"`
	Path   string    `json:"path"`
	Status int       `json:"status"`
	Text   string    `json:"text"`
}

// BlockedError reports a block, either the response just received or a marker left by an earlier one.
type BlockedError struct {
	Marker     Marker
	MarkerPath string
	// WriteErr is set when the marker could not be written.
	WriteErr error
}

func (e *BlockedError) Error() string {
	msg := fmt.Sprintf("Doctolib blocked a request (HTTP %d on %s at %s): %q; marker %s",
		e.Marker.Status, e.Marker.Path, e.Marker.Time.Format(time.RFC3339), e.Marker.Text, e.MarkerPath)
	if e.WriteErr != nil {
		msg += fmt.Sprintf(" (writing it failed: %v)", e.WriteErr)
	}
	return msg
}

// BudgetError reports that a cap is reached.
type BudgetError struct {
	// Window is "hour" or "day".
	Window string
	Limit  int
	// Resume is when the next request will be let through.
	Resume time.Time
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("the cap of %d Doctolib requests per %s is reached until %s",
		e.Limit, e.Window, e.Resume.Format(time.RFC3339))
}

// MarkerPath is the block marker file; only a person deletes it.
func (g *Gate) MarkerPath() string {
	return filepath.Join(g.dir, markerFile)
}

// Acquire returns once a request may be sent and records it as sent.
//
// It refuses while the block marker exists or a cap is reached, and otherwise
// waits until the interval since the last request has passed. It waits
// holding the lock, so the process already waiting goes first, and checks the
// marker again on waking, so a block written meanwhile stops it.
func (g *Gate) Acquire(ctx context.Context) error {
	unlock, err := g.lock()
	if err != nil {
		return err
	}
	defer unlock()

	err = g.checkMarker()
	if err != nil {
		return err
	}
	sent, err := g.readLog()
	if err != nil {
		return err
	}
	now := g.now()
	sent = since(sent, now.Add(-day))
	err = g.checkCaps(sent, now)
	if err != nil {
		return err
	}
	if n := len(sent); n > 0 {
		now, err = g.waitAfter(ctx, sent[n-1], now)
		if err != nil {
			return err
		}
	}
	return g.writeLog(append(sent, now))
}

// waitAfter waits until the interval after the last request has passed, and
// returns the time to record. It waits at most one interval and records no
// time before last, even after the clock steps back, so the log stays in order.
func (g *Gate) waitAfter(ctx context.Context, last, now time.Time) (time.Time, error) {
	if wait := min(last.Add(g.interval).Sub(now), g.interval); wait > 0 {
		err := g.sleep(ctx, wait)
		if err != nil {
			return now, err
		}
		err = g.checkMarker()
		if err != nil {
			return now, err
		}
		now = g.now()
	}
	if now.Before(last) {
		return last, nil
	}
	return now, nil
}

// Block writes the block marker for the response to path, stamped with the
// gate's clock, and returns it. From then on every Acquire refuses until a
// person deletes it.
//
// It takes no lock: a process waiting out the interval holds it, and must find
// the marker when it wakes. Replacing the file by a rename keeps the write whole.
func (g *Gate) Block(path string, status int, text string) (Marker, error) {
	m := Marker{Time: g.now(), Path: path, Status: status, Text: text}
	err := os.MkdirAll(g.dir, 0o700)
	if err != nil {
		return m, fmt.Errorf("create state directory: %w", err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return m, fmt.Errorf("encode block marker: %w", err)
	}
	return m, writeFile(g.MarkerPath(), data)
}

func (g *Gate) checkCaps(sent []time.Time, now time.Time) error {
	if g.hourly > 0 {
		lastHour := since(sent, now.Add(-time.Hour))
		if len(lastHour) >= g.hourly {
			resume := lastHour[len(lastHour)-g.hourly].Add(time.Hour)
			return &BudgetError{Window: "hour", Limit: g.hourly, Resume: resume}
		}
	}
	if g.daily > 0 && len(sent) >= g.daily {
		return &BudgetError{Window: "day", Limit: g.daily, Resume: sent[len(sent)-g.daily].Add(day)}
	}
	return nil
}

// lock takes the exclusive lock on the state directory, creating the directory if needed.
func (g *Gate) lock() (func(), error) {
	err := os.MkdirAll(g.dir, 0o700)
	if err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(g.dir, lockFile), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	fd := int(f.Fd())
	err = syscall.Flock(fd, syscall.LOCK_EX)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", f.Name(), err)
	}
	return func() {
		_ = syscall.Flock(fd, syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// checkMarker returns a [BlockedError] while the block marker exists.
func (g *Gate) checkMarker() error {
	marker, blocked, err := g.readMarker()
	if err != nil {
		return err
	}
	if blocked {
		return &BlockedError{Marker: marker, MarkerPath: g.MarkerPath(), WriteErr: nil}
	}
	return nil
}

// readMarker reports whether the block marker exists, and what it holds.
func (g *Gate) readMarker() (Marker, bool, error) {
	var m Marker
	data, err := os.ReadFile(g.MarkerPath())
	if errors.Is(err, fs.ErrNotExist) {
		return m, false, nil
	}
	if err != nil {
		return m, false, fmt.Errorf("read block marker: %w", err)
	}
	if json.Unmarshal(data, &m) != nil {
		// The file still blocks: its presence is what counts.
		m = Marker{Time: time.Time{}, Path: "", Status: 0, Text: string(data)}
	}
	return m, true, nil
}

func (g *Gate) readLog() ([]time.Time, error) {
	data, err := os.ReadFile(filepath.Join(g.dir, logFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read request log: %w", err)
	}
	var sent []time.Time
	err = json.Unmarshal(data, &sent)
	if err != nil {
		return nil, fmt.Errorf("decode request log %s: %w", filepath.Join(g.dir, logFile), err)
	}
	return sent, nil
}

func (g *Gate) writeLog(sent []time.Time) error {
	data, err := json.Marshal(sent)
	if err != nil {
		return fmt.Errorf("encode request log: %w", err)
	}
	return writeFile(filepath.Join(g.dir, logFile), data)
}

// since returns the times after from; sent is in ascending order.
func since(sent []time.Time, from time.Time) []time.Time {
	for i, t := range sent {
		if t.After(from) {
			return sent[i:]
		}
	}
	return nil
}

// writeFile replaces path atomically, so a crash never leaves half a file:
// the data reaches the disk before the rename makes it the file.
func writeFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write %s: %w", path, err)
	}
	err = os.Rename(tmp.Name(), path)
	if err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	// The rename reaches the disk with the directory. Best effort: some
	// systems cannot sync a directory, and the file itself is already synced.
	if dir, openErr := os.Open(filepath.Dir(path)); openErr == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait for the request interval: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

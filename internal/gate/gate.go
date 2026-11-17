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
}

// Gate lets a request through once the block marker, the caps and the spacing allow it.
type Gate struct {
	dir      string
	interval time.Duration
	hourly   int
	daily    int
	now      func() time.Time
}

// New returns a gate over the state directory c.Dir.
func New(c Config) *Gate {
	now := c.Now
	if now == nil {
		now = time.Now
	}
	return &Gate{dir: c.Dir, interval: c.Interval, hourly: c.Hourly, daily: c.Daily, now: now}
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
// waits until the interval since the last request has passed.
func (g *Gate) Acquire(ctx context.Context) error {
	unlock, err := g.lock()
	if err != nil {
		return err
	}
	defer unlock()

	marker, err := g.readMarker()
	if err != nil {
		return err
	}
	if marker != nil {
		return &BlockedError{Marker: *marker, MarkerPath: g.MarkerPath(), WriteErr: nil}
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
		if wait := sent[n-1].Add(g.interval).Sub(now); wait > 0 {
			err = sleep(ctx, wait)
			if err != nil {
				return err
			}
			now = g.now()
		}
	}
	return g.writeLog(append(sent, now))
}

// Block writes the block marker. From then on every Acquire refuses until a person deletes it.
func (g *Gate) Block(m Marker) error {
	unlock, err := g.lock()
	if err != nil {
		return err
	}
	defer unlock()
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode block marker: %w", err)
	}
	return writeFile(g.MarkerPath(), data)
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

func (g *Gate) readMarker() (*Marker, error) {
	data, err := os.ReadFile(g.MarkerPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil //nolint:nilnil // no marker is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("read block marker: %w", err)
	}
	var m Marker
	if json.Unmarshal(data, &m) != nil {
		// The file still blocks: its presence is what counts.
		m = Marker{Time: time.Time{}, Path: "", Status: 0, Text: string(data)}
	}
	return &m, nil
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

// writeFile replaces path atomically, so a crash never leaves half a file.
func writeFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	_, err = tmp.Write(data)
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

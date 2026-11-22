// Package gate spaces and caps the requests to Doctolib. Every server process of the user shares one state
// directory and file lock, since they all go out through the same IP address.
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

// Sized on heavy manual use: about 30 result pages an hour, each firing about 20 slot requests.
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

type Config struct {
	Dir      string
	Interval time.Duration
	// Hourly and Daily cap the requests over the last hour and the last 24 hours, both rolling.
	Hourly int
	Daily  int
	Now    func() time.Time
	Sleep  func(ctx context.Context, d time.Duration) error
}

type Gate struct {
	dir      string
	interval time.Duration
	hourly   int
	daily    int
	now      func() time.Time
	sleep    func(ctx context.Context, d time.Duration) error
}

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

// DefaultDir ignores a relative XDG_STATE_HOME, as the XDG Base Directory spec requires: resolved against each
// process's working directory, it would give every client its own limits and block marker.
func DefaultDir() (string, error) {
	if base := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(base) {
		return filepath.Join(base, "doctolib-mcp"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find the state directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "doctolib-mcp"), nil
}

type Marker struct {
	Time   time.Time `json:"time"`
	Path   string    `json:"path"`
	Status int       `json:"status"`
	Text   string    `json:"text"`
}

type BlockedError struct {
	Marker     Marker
	MarkerPath string
	WriteErr   error
}

func (e *BlockedError) Error() string {
	msg := fmt.Sprintf("Doctolib blocked a request (HTTP %d on %s at %s): %q; marker %s",
		e.Marker.Status, e.Marker.Path, e.Marker.Time.Format(time.RFC3339), e.Marker.Text, e.MarkerPath)
	if e.WriteErr != nil {
		msg += fmt.Sprintf(" (writing it failed: %v)", e.WriteErr)
	}
	return msg
}

type BudgetError struct {
	Window string
	Limit  int
	Resume time.Time
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("the cap of %d Doctolib requests per %s is reached until %s",
		e.Limit, e.Window, e.Resume.Format(time.RFC3339))
}

func (g *Gate) MarkerPath() string {
	return filepath.Join(g.dir, markerFile)
}

// Acquire waits out the interval holding the lock, so the first to wait goes first, and checks the marker
// again on waking.
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

// CheckMarker is for a call that sends no request: it only reads the marker, so it counts against no cap and
// waits out no interval. Block replaces the marker atomically, so it needs no lock.
func (g *Gate) CheckMarker() error {
	return g.checkMarker()
}

// waitAfter waits at most one interval and returns no time before last, so a clock stepping back neither
// stalls the gate nor puts the log out of order.
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

// Block takes no lock: a process sleeping out the interval holds it and must
// see the marker when it wakes.
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

// since needs sent in ascending order.
func since(sent []time.Time, from time.Time) []time.Time {
	for i, t := range sent {
		if t.After(from) {
			return sent[i:]
		}
	}
	return nil
}

// writeFile replaces path atomically.
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
	// Syncing the directory is best effort: some systems can't.
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

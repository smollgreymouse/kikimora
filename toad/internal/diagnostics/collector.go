// Package diagnostics keeps a bounded, rolling capture of the host journal and
// the product logs so a core, network or UI failure can still be explained
// after the fact.
//
// The capture is off by default and controlled from the desktop UI. When it is
// on, a collector appends the system journal, the Kikimora core and Toad logs,
// the Leshy logs and the desktop UI log into hourly files under one directory,
// deletes everything older than 24 hours and caps the spool size. Nothing here
// reads secrets: the collector copies log files and asks the host for journal
// records, it never opens tunnel or endpoint configuration.
package diagnostics

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// Schema is the on-disk settings schema.
	Schema = 1
	// DefaultInterval is how often the collector appends new records.
	DefaultInterval = time.Minute
	// DefaultRetention is how much history the spool keeps.
	DefaultRetention = 24 * time.Hour

	// maxChunkBytes bounds one append per source so a log burst cannot blow up
	// a single collection run.
	maxChunkBytes = 1 << 20
	// maxSpoolBytes bounds the whole spool; the oldest files go first.
	maxSpoolBytes = 256 << 20
)

// Settings is the persisted on/off choice and survives restarts.
type Settings struct {
	Schema    int       `json:"schema"`
	Enabled   bool      `json:"enabled"`
	UpdatedAt time.Time `json:"updated_at"`
}

// FileStatus describes one spool file for the UI.
type FileStatus struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Status is what the core publishes to the UI.
type Status struct {
	Enabled   bool         `json:"enabled"`
	Directory string       `json:"directory"`
	LastRun   time.Time    `json:"last_run,omitempty"`
	LastError string       `json:"last_error,omitempty"`
	Files     []FileStatus `json:"files,omitempty"`
	Bytes     int64        `json:"bytes"`
}

// Hooks let the core add its own textual state without importing the control
// plane from this package.
type Hooks struct {
	// Snapshot returns a short, secret-free description of the current product
	// state (versions, aggregate state, roles, underlay). It is appended to the
	// spool on every run.
	Snapshot func() string
	// HostInfo returns OS and kernel details; optional.
	HostInfo func() string
}

// Collector owns the spool directory and the on/off switch.
type Collector struct {
	dir       string
	interval  time.Duration
	retention time.Duration
	now       func() time.Time
	hooks     Hooks
	// hostSources toggles journal/event-log and network probes; tests turn
	// them off so a unit test never shells out to the host.
	hostSources bool

	mu        sync.Mutex
	enabled   bool
	lastRun   time.Time
	lastError string
	cursor    time.Time
	offsets   map[string]int64

	runMu sync.Mutex
}

type persistedState struct {
	Cursor  time.Time        `json:"cursor,omitempty"`
	Offsets map[string]int64 `json:"offsets,omitempty"`
}

// New opens (and creates) the spool directory.
func New(dir string, hooks Hooks) (*Collector, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("diagnostics directory is empty")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create diagnostics directory: %w", err)
	}
	c := &Collector{
		dir:         dir,
		interval:    DefaultInterval,
		retention:   DefaultRetention,
		now:         time.Now,
		hooks:       hooks,
		hostSources: true,
		offsets:     map[string]int64{},
	}
	settings, err := c.loadSettings()
	if err != nil {
		return nil, err
	}
	c.enabled = settings.Enabled
	state, err := c.loadState()
	if err != nil {
		return nil, err
	}
	c.cursor = state.Cursor
	if state.Offsets != nil {
		c.offsets = state.Offsets
	}
	return c, nil
}

// Directory is the spool root.
func (c *Collector) Directory() string { return c.dir }

// SetHostSources disables the journal, event-log and network probes; tests use
// it so a unit test never depends on the host.
func (c *Collector) SetHostSources(enabled bool) { c.hostSources = enabled }

// SetInterval overrides the collection cadence (tests).
func (c *Collector) SetInterval(interval time.Duration) {
	if interval > 0 {
		c.interval = interval
	}
}

// Enabled reports the persisted switch.
func (c *Collector) Enabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enabled
}

// SetEnabled persists the switch. Turning it on collects immediately so the
// operator does not have to wait a full interval.
func (c *Collector) SetEnabled(ctx context.Context, enabled bool) error {
	c.mu.Lock()
	changed := c.enabled != enabled
	c.enabled = enabled
	c.mu.Unlock()
	settings := Settings{Schema: Schema, Enabled: enabled, UpdatedAt: c.now().UTC()}
	if err := writeJSONAtomic(c.settingsPath(), settings); err != nil {
		return fmt.Errorf("persist diagnostics settings: %w", err)
	}
	if enabled && changed {
		return c.Collect(ctx)
	}
	return nil
}

// Run collects on a timer until ctx is cancelled. A disabled collector keeps
// the loop alive so enabling from the UI takes effect without a restart.
func (c *Collector) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	if c.Enabled() {
		_ = c.Collect(ctx)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if c.Enabled() {
				_ = c.Collect(ctx)
			}
		}
	}
}

// Collect appends every source once and prunes the spool.
func (c *Collector) Collect(ctx context.Context) error {
	c.runMu.Lock()
	defer c.runMu.Unlock()

	if !c.Enabled() {
		return nil
	}
	now := c.now().UTC()
	var failures []string

	journalOK := true
	if err := c.collectJournal(ctx, now); err != nil {
		if !errors.Is(err, errSourceUnavailable) {
			journalOK = false
			failures = append(failures, "journal: "+err.Error())
		}
	}
	eventOK := true
	if err := c.collectEventLog(ctx, now); err != nil {
		if !errors.Is(err, errSourceUnavailable) {
			eventOK = false
			failures = append(failures, "event log: "+err.Error())
		}
	}
	if journalOK && eventOK {
		// Advance only after a successful read: a failed journal call must not
		// move the cursor past records that were never captured.
		c.advanceCursor(now)
	}
	if err := c.collectFiles(now); err != nil {
		failures = append(failures, "logs: "+err.Error())
	}
	c.collectState(now)

	if err := c.prune(now); err != nil {
		failures = append(failures, "prune: "+err.Error())
	}
	c.mu.Lock()
	c.lastRun = now
	c.lastError = strings.Join(failures, "; ")
	c.mu.Unlock()
	_ = c.saveState()
	return nil
}

var errSourceUnavailable = errors.New("source unavailable on this platform")

func (c *Collector) collectJournal(ctx context.Context, now time.Time) error {
	if !c.hostSources || runtime.GOOS == "windows" {
		return errSourceUnavailable
	}
	path, err := exec.LookPath("journalctl")
	if err != nil {
		return errSourceUnavailable
	}
	since := c.since(now)
	args := []string{"-q", "--since", "@" + fmt.Sprint(since.Unix()), "-o", "short-iso"}
	units := []string{"kikimora-core.service", "leshy.service", "kikimora-leshy-dns.service", "NetworkManager.service"}
	for _, unit := range units {
		args = append(args, "-u", unit)
	}
	out, err := runLimited(ctx, path, args...)
	if err != nil {
		return err
	}
	// The kernel ring buffer carries the link, route and DNS failures that never
	// reach a unit. Keep it in its own file so the unit stream stays readable.
	kernelArgs := []string{"-q", "--since", "@" + fmt.Sprint(since.Unix()), "-o", "short-iso", "-k"}
	kernelOut, kernelErr := runLimited(ctx, path, kernelArgs...)
	if kernelErr != nil {
		kernelOut = nil
	}
	if len(out) > 0 {
		if err := c.appendSpool("journal", now, out); err != nil {
			return err
		}
	}
	if len(kernelOut) > 0 {
		if err := c.appendSpool("kernel", now, kernelOut); err != nil {
			return err
		}
	}
	return nil
}

func (c *Collector) collectEventLog(ctx context.Context, now time.Time) error {
	if !c.hostSources || runtime.GOOS != "windows" {
		return errSourceUnavailable
	}
	path, err := exec.LookPath("wevtutil")
	if err != nil {
		return errSourceUnavailable
	}
	since := c.since(now)
	stamp := since.Format("2006-01-02T15:04:05.000Z")
	query := fmt.Sprintf("*[System[TimeCreated[@SystemTime>='%s']]]", stamp)
	for _, channel := range []string{"System", "Application"} {
		args := []string{"qe", channel, "/f:text", "/rd:false", "/c:500", "/q:" + query}
		out, runErr := runLimited(ctx, path, args...)
		if runErr != nil {
			continue
		}
		if len(out) > 0 {
			if err := c.appendSpool("eventlog-"+strings.ToLower(channel), now, out); err != nil {
				return err
			}
		}
	}
	return nil
}

// since returns the cursor with a small overlap so a record written while the
// previous run was reading is not lost; duplicates are cheaper than gaps.
func (c *Collector) since(now time.Time) time.Time {
	c.mu.Lock()
	cursor := c.cursor
	c.mu.Unlock()
	if cursor.IsZero() || now.Sub(cursor) > c.retention {
		return now.Add(-c.retention)
	}
	return cursor.Add(-5 * time.Second)
}

func (c *Collector) collectFiles(now time.Time) error {
	var failures []string
	for _, pattern := range logGlobs() {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, path := range matches {
			if err := c.tailFile(path, now); err != nil {
				failures = append(failures, filepath.Base(path)+": "+err.Error())
			}
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

// tailFile appends the bytes written since the previous run. A truncated or
// rotated file restarts from the beginning.
func (c *Collector) tailFile(path string, now time.Time) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.IsDir() {
		return nil
	}
	c.mu.Lock()
	offset := c.offsets[path]
	c.mu.Unlock()
	if info.Size() < offset {
		offset = 0
	}
	if info.Size() == offset {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	start := offset
	length := info.Size() - offset
	if length > maxChunkBytes {
		start = info.Size() - maxChunkBytes
		length = maxChunkBytes
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return err
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(file, data); err != nil {
		return err
	}
	header := fmt.Sprintf("# %s @ %s (+%d bytes, offset %d)\n", path, now.Format(time.RFC3339), length, start)
	if err := c.appendSpool("logs", now, append([]byte(header), data...)); err != nil {
		return err
	}
	c.mu.Lock()
	c.offsets[path] = info.Size()
	c.mu.Unlock()
	return nil
}

func (c *Collector) collectState(now time.Time) {
	var builder strings.Builder
	fmt.Fprintf(&builder, "=== diagnostics snapshot %s ===\n", now.Format(time.RFC3339))
	if c.hooks.HostInfo != nil {
		builder.WriteString(c.hooks.HostInfo())
		builder.WriteString("\n")
	}
	if c.hooks.Snapshot != nil {
		builder.WriteString(c.hooks.Snapshot())
		builder.WriteString("\n")
	}
	if c.hostSources {
		builder.WriteString(networkState())
	}
	_ = c.appendSpool("state", now, []byte(builder.String()))
}

// appendSpool appends to the current hourly file of one source.
func (c *Collector) appendSpool(source string, now time.Time, data []byte) error {
	name := fmt.Sprintf("%s-%s.log", source, now.UTC().Format("20060102T15"))
	path := filepath.Join(c.dir, name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// prune deletes files older than the retention window and then, if the spool
// is still too large, the oldest files until it fits.
func (c *Collector) prune(now time.Time) error {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return err
	}
	type fileInfo struct {
		path    string
		size    int64
		modTime time.Time
	}
	var files []fileInfo
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > c.retention {
			_ = os.Remove(filepath.Join(c.dir, entry.Name()))
			continue
		}
		files = append(files, fileInfo{filepath.Join(c.dir, entry.Name()), info.Size(), info.ModTime()})
		total += info.Size()
	}
	if total <= maxSpoolBytes {
		return nil
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })
	for _, file := range files {
		if total <= maxSpoolBytes {
			break
		}
		if err := os.Remove(file.path); err == nil {
			total -= file.size
		}
	}
	return nil
}

// Status returns the UI-facing state, including the spool inventory.
func (c *Collector) Status() Status {
	c.mu.Lock()
	status := Status{
		Enabled:   c.enabled,
		Directory: c.dir,
		LastRun:   c.lastRun,
		LastError: c.lastError,
	}
	c.mu.Unlock()
	entries, err := os.ReadDir(c.dir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			status.Files = append(status.Files, FileStatus{
				Name:      entry.Name(),
				Size:      info.Size(),
				UpdatedAt: info.ModTime(),
			})
			status.Bytes += info.Size()
		}
	}
	sort.Slice(status.Files, func(i, j int) bool { return status.Files[i].Name > status.Files[j].Name })
	if len(status.Files) > 12 {
		status.Files = status.Files[:12]
	}
	return status
}

// Bundle writes every spool file into one zip archive and returns its path.
func (c *Collector) Bundle(out string) (string, error) {
	if strings.TrimSpace(out) == "" {
		out = filepath.Join(c.dir, fmt.Sprintf("kikimora-diagnostics-%s.zip", c.now().UTC().Format("20060102-150405")))
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return "", err
	}
	file, err := os.Create(out)
	if err != nil {
		return "", err
	}
	writer := zip.NewWriter(file)
	var failures []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		source, err := os.Open(filepath.Join(c.dir, entry.Name()))
		if err != nil {
			failures = append(failures, entry.Name())
			continue
		}
		target, err := writer.Create(entry.Name())
		if err == nil {
			_, err = io.Copy(target, source)
		}
		_ = source.Close()
		if err != nil {
			failures = append(failures, entry.Name())
		}
	}
	if err := writer.Close(); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if len(failures) > 0 {
		return out, fmt.Errorf("skipped %d file(s): %s", len(failures), strings.Join(failures, ", "))
	}
	return out, nil
}

func (c *Collector) settingsPath() string {
	return filepath.Join(c.dir, "settings.json")
}

func (c *Collector) statePath() string {
	return filepath.Join(c.dir, "offsets.json")
}

func (c *Collector) loadSettings() (Settings, error) {
	data, err := os.ReadFile(c.settingsPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Settings{Schema: Schema}, nil
		}
		return Settings{}, err
	}
	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return Settings{}, fmt.Errorf("decode diagnostics settings: %w", err)
	}
	if settings.Schema != Schema {
		return Settings{}, fmt.Errorf("unsupported diagnostics settings schema %d", settings.Schema)
	}
	return settings, nil
}

func (c *Collector) loadState() (persistedState, error) {
	data, err := os.ReadFile(c.statePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return persistedState{}, nil
		}
		return persistedState{}, err
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		// A corrupt cursor must not stop collection: restart from the retention
		// window rather than refusing to collect at all.
		return persistedState{}, nil
	}
	return state, nil
}

func (c *Collector) saveState() error {
	c.mu.Lock()
	state := persistedState{Cursor: c.cursor, Offsets: make(map[string]int64, len(c.offsets))}
	for path, offset := range c.offsets {
		state.Offsets[path] = offset
	}
	c.mu.Unlock()
	return writeJSONAtomic(c.statePath(), state)
}

func (c *Collector) advanceCursor(now time.Time) {
	c.mu.Lock()
	if now.After(c.cursor) {
		c.cursor = now
	}
	c.mu.Unlock()
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".diagnostics-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// runLimited runs a command with a hard timeout and returns its combined
// output capped at a sane size.
func runLimited(ctx context.Context, name string, args ...string) ([]byte, error) {
	runCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, name, args...)
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(output) > 0 {
			return output, nil
		}
		return nil, err
	}
	if len(output) > 4*maxChunkBytes {
		output = output[len(output)-4*maxChunkBytes:]
	}
	return output, nil
}

package diagnostics

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestCollector(t *testing.T) (*Collector, string) {
	t.Helper()
	dir := t.TempDir()
	collector, err := New(dir, Hooks{Snapshot: func() string { return "aggregate=Ready" }})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	collector.SetHostSources(false)
	return collector, dir
}

func spoolFiles(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.log"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func TestCollectorIsDisabledByDefault(t *testing.T) {
	collector, dir := newTestCollector(t)
	if collector.Enabled() {
		t.Fatal("a fresh collector must be disabled")
	}
	if err := collector.Collect(context.Background()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if files := spoolFiles(t, dir); len(files) != 0 {
		t.Fatalf("disabled collector wrote %v", files)
	}
}

func TestEnablePersistsAndCollects(t *testing.T) {
	collector, dir := newTestCollector(t)
	if err := collector.SetEnabled(context.Background(), true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if !collector.Enabled() {
		t.Fatal("collector did not enable")
	}
	data, err := os.ReadFile(filepath.Join(dir, "state-"+collector.now().UTC().Format("20060102T15")+".log"))
	if err != nil {
		t.Fatalf("state spool: %v", err)
	}
	if !strings.Contains(string(data), "aggregate=Ready") {
		t.Fatalf("state spool missing the snapshot: %q", data)
	}

	reopened, err := New(dir, Hooks{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if !reopened.Enabled() {
		t.Fatal("enabled state did not survive a restart")
	}
}

func TestTailFileAppendsOnlyNewBytes(t *testing.T) {
	collector, dir := newTestCollector(t)
	logPath := filepath.Join(t.TempDir(), "core.log")
	if err := os.WriteFile(logPath, []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Exercise the tail path directly: host globs are platform specific.
	now := collector.now().UTC()
	if err := collector.tailFile(logPath, now); err != nil {
		t.Fatalf("tailFile: %v", err)
	}
	if err := collector.tailFile(logPath, now); err != nil {
		t.Fatalf("tailFile idempotent: %v", err)
	}
	spool := filepath.Join(dir, "logs-"+now.Format("20060102T15")+".log")
	data, err := os.ReadFile(spool)
	if err != nil {
		t.Fatalf("read spool: %v", err)
	}
	if strings.Count(string(data), "first") != 1 {
		t.Fatalf("second run duplicated the line: %q", data)
	}

	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("second\n"); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if err := collector.tailFile(logPath, now); err != nil {
		t.Fatalf("tailFile append: %v", err)
	}
	data, err = os.ReadFile(spool)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "second") {
		t.Fatalf("new bytes missing: %q", data)
	}
	if strings.Count(string(data), "first") != 1 {
		t.Fatalf("old bytes replayed: %q", data)
	}
}

func TestTailFileRestartsAfterTruncate(t *testing.T) {
	collector, dir := newTestCollector(t)
	logPath := filepath.Join(t.TempDir(), "leshy.log")
	if err := os.WriteFile(logPath, []byte("before-rotation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := collector.now().UTC()
	if err := collector.tailFile(logPath, now); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := collector.tailFile(logPath, now); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "logs-"+now.Format("20060102T15")+".log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "after") {
		t.Fatalf("rotated file was not re-read: %q", data)
	}
}

func TestPruneDeletesFilesOlderThanRetention(t *testing.T) {
	collector, dir := newTestCollector(t)
	old := filepath.Join(dir, "journal-20260101T00.log")
	if err := os.WriteFile(old, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(old, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(dir, "journal-20260101T01.log")
	if err := os.WriteFile(fresh, []byte("fresh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := collector.prune(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("stale spool file survived: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh spool file was removed: %v", err)
	}
}

func TestStatusReportsInventory(t *testing.T) {
	collector, dir := newTestCollector(t)
	if err := collector.SetEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	status := collector.Status()
	if !status.Enabled || status.Directory != dir {
		t.Fatalf("status: %+v", status)
	}
	if len(status.Files) == 0 || status.Bytes == 0 {
		t.Fatalf("status inventory is empty: %+v", status)
	}
	if status.LastRun.IsZero() {
		t.Fatal("last run is not recorded")
	}
}

func TestBundleZipsTheSpool(t *testing.T) {
	collector, dir := newTestCollector(t)
	if err := collector.SetEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "bundle.zip")
	path, err := collector.Bundle(out)
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open bundle: %v", err)
	}
	defer reader.Close()
	if len(reader.File) == 0 {
		t.Fatal("bundle is empty")
	}
	names := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		names = append(names, file.Name)
	}
	if !strings.Contains(strings.Join(names, ","), "state-") {
		t.Fatalf("bundle misses the state spool: %v", names)
	}
	if files := spoolFiles(t, dir); len(files) == 0 {
		t.Fatal("spool disappeared")
	}
}

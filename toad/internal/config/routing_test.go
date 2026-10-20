package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRoutingEntry(t *testing.T) {
	valid := []string{
		"example.com",
		"*.example.com",
		"sub.example.co.uk",
		"10.0.0.0/8",
		"192.168.1.0/24",
		"1.2.3.4",
		"2a00:1370::/32",
	}
	for _, entry := range valid {
		if err := ValidateRoutingEntry(entry); err != nil {
			t.Fatalf("%q rejected: %v", entry, err)
		}
	}

	invalid := []string{
		"",
		"http://example.com/path",
		"example",
		"exa mple.com",
		"10.0.0.0/33",
		"***.example.com",
	}
	for _, entry := range invalid {
		if err := ValidateRoutingEntry(entry); err == nil {
			t.Fatalf("%q accepted", entry)
		}
	}
}

func TestWriteAndReadRoutingList(t *testing.T) {
	dir := t.TempDir()
	content := "# operator notes\nExample.COM\n*.OpenAI.com\n10.8.0.0/16\n"

	if err := WriteRoutingList(dir, RoutingPrimary, content); err != nil {
		t.Fatalf("WriteRoutingList: %v", err)
	}
	read, err := ReadRoutingList(dir, RoutingPrimary)
	if err != nil {
		t.Fatalf("ReadRoutingList: %v", err)
	}
	expected := "example.com\n*.openai.com\n10.8.0.0/16\n"
	if read != expected {
		t.Fatalf("content = %q, want %q", read, expected)
	}
	if _, err := os.Stat(filepath.Join(dir, "primary.txt.tmp")); !os.IsNotExist(err) {
		t.Fatal("temporary file left behind")
	}
}

func TestWriteRoutingListRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	if err := WriteRoutingList(dir, RoutingBypass, "good.com\nhttp://bad/path\n"); err == nil {
		t.Fatal("garbage accepted")
	}
	if err := WriteRoutingList(dir, RoutingList("nonsense"), "good.com\n"); err == nil {
		t.Fatal("unknown list accepted")
	}
	content, err := ReadRoutingList(dir, RoutingBypass)
	if err != nil {
		t.Fatal(err)
	}
	if content != "" {
		t.Fatalf("rejected write changed the list: %q", content)
	}
}

func TestReadRoutingListMissingIsEmpty(t *testing.T) {
	dir := t.TempDir()
	content, err := ReadRoutingList(dir, RoutingSecondary)
	if err != nil {
		t.Fatalf("missing list returned an error: %v", err)
	}
	if strings.TrimSpace(content) != "" {
		t.Fatalf("missing list returned %q", content)
	}
}

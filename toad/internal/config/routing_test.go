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

	if err := WriteRoutingList(dir, "awg-main", content); err != nil {
		t.Fatalf("WriteRoutingList: %v", err)
	}
	read, err := ReadRoutingList(dir, "awg-main")
	if err != nil {
		t.Fatalf("ReadRoutingList: %v", err)
	}
	expected := "# operator notes\nexample.com\n*.openai.com\n10.8.0.0/16\n"
	if read != expected {
		t.Fatalf("content = %q, want %q", read, expected)
	}
	if _, err := os.Stat(filepath.Join(dir, "primary.txt.tmp")); !os.IsNotExist(err) {
		t.Fatal("temporary file left behind")
	}
}

func TestWriteRoutingListRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	if err := WriteRoutingList(dir, "awg-main", "good.com\nhttp://bad/path\n"); err == nil {
		t.Fatal("garbage accepted")
	}
	if err := WriteRoutingList(dir, "bad name", "good.com\n"); err == nil {
		t.Fatal("unknown list accepted")
	}
	content, err := ReadRoutingList(dir, "awg-main")
	if err != nil {
		t.Fatal(err)
	}
	if content != "" {
		t.Fatalf("rejected write changed the list: %q", content)
	}
}

func TestReadRoutingListMissingIsEmpty(t *testing.T) {
	dir := t.TempDir()
	content, err := ReadRoutingList(dir, "oc-main")
	if err != nil {
		t.Fatalf("missing list returned an error: %v", err)
	}
	if strings.TrimSpace(content) != "" {
		t.Fatalf("missing list returned %q", content)
	}
}

func TestCommentsSurviveSave(t *testing.T) {
	dir := t.TempDir()
	original := "# Заголовок пакета\n# ещё пояснение\nexample.com\n\n10.8.0.0/16\n"
	if err := WriteRoutingList(dir, "awg-main", original); err != nil {
		t.Fatalf("WriteRoutingList: %v", err)
	}
	content, err := ReadRoutingList(dir, "awg-main")
	if err != nil {
		t.Fatal(err)
	}
	expected := "# Заголовок пакета\n# ещё пояснение\nexample.com\n10.8.0.0/16\n"
	if content != expected {
		t.Fatalf("comments lost:\n got %q\nwant %q", content, expected)
	}

	// A save that only touches entries keeps the comments exactly as they were.
	if err := WriteRoutingList(dir, "awg-main", content+"*.openai.com\n"); err != nil {
		t.Fatalf("second save: %v", err)
	}
	again, err := ReadRoutingList(dir, "awg-main")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(again, "# Заголовок пакета") || !strings.Contains(again, "*.openai.com") {
		t.Fatalf("comments or entry lost on the second save: %q", again)
	}
}

func TestBOMListIsAccepted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "awg.txt")
	// Notepad and friends love a byte order mark; it must not break the first line.
	if err := os.WriteFile(path, append([]byte{0xEF, 0xBB, 0xBF}, []byte("chatgpt.com\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	content, err := ReadRoutingList(dir, "awg")
	if err != nil {
		t.Fatalf("ReadRoutingList: %v", err)
	}
	if _, err := ValidateRoutingContent(content); err != nil {
		t.Fatalf("BOM broke validation: %v", err)
	}
}

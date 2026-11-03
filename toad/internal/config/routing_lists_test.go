package config

import (
	"testing"
)

func TestRoleIPv6SettingsRoundTripAndDefault(t *testing.T) {
	dir := t.TempDir()
	// A fresh directory reads as the product default (no entries).
	settings := ReadRoleIPv6Settings(dir)
	if _, ok := settings["primary"]; ok {
		t.Fatal("a fresh directory must have no entries")
	}
	if _, err := WriteRoleIPv6Setting(dir, "primary", false); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteRoleIPv6Setting(dir, "secondary", true); err != nil {
		t.Fatal(err)
	}
	settings = ReadRoleIPv6Settings(dir)
	if settings["primary"] || !settings["secondary"] {
		t.Fatalf("settings round-trip mismatch: %#v", settings)
	}
}

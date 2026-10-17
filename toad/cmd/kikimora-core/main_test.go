package main

import (
	"strings"
	"testing"
)

func TestRoleCommandRequiresRole(t *testing.T) {
	for _, method := range []string{"ConnectRole", "DisconnectRole", "RetryRole"} {
		err := roleCommand(method, nil)
		if err == nil || !strings.Contains(err.Error(), "-role is required") {
			t.Fatalf("%s without -role: want required error, got %v", method, err)
		}
	}
}

func TestVersionAndHelpDoNotNeedSocket(t *testing.T) {
	if err := version(nil); err != nil {
		t.Fatalf("version: %v", err)
	}
	if err := help(nil); err != nil {
		t.Fatalf("help: %v", err)
	}
}

func TestProfilesUseRequiresName(t *testing.T) {
	if err := profilesUse(nil); err == nil {
		t.Fatal("profiles use without a name must fail")
	}
}

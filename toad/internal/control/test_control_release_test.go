//go:build !kikimora_test_failpoints

package control

import (
	"context"
	"testing"
)

// TestReleaseBuildRejectsTestTriggerMethod pins the release-side half of the
// R3 contract: the normal (untagged) build must answer the tagged test
// method with the ordinary unsupported-method failure, because the release
// handler never handles anything. The tagged build proves the same method
// reaches the real production recovery path.
func TestReleaseBuildRejectsTestTriggerMethod(t *testing.T) {
	dir := shortSocketDir(t)
	manager, err := NewManager([]string{writeConfig(t, dir, "primary", "openconnect")}, &fakeLauncher{}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	response := manager.Handle(context.Background(), Request{
		Version: APIVersion,
		Method:  "TestTriggerManagedRecovery",
		Role:    "primary",
	})
	if response.OK {
		t.Fatal("the release build must not schedule a recovery for the test method")
	}
	if response.Error != `unsupported method "TestTriggerManagedRecovery"` {
		t.Fatalf("error = %q, want the unsupported-method failure", response.Error)
	}
}

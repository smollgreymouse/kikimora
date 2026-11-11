//go:build kikimora_test_failpoints

package control

import (
	"context"
	"fmt"
)

// testControlMethodName is the one tagged control method the test build
// recognizes. It is never added to Handshake capabilities and never compiled
// into a release binary.
const testControlMethodName = "TestTriggerManagedRecovery"

// handleTestControlMethod routes the tagged test method into the real
// production recovery request path (R3 §5.3): the request must reach
// Manager.requestManagedSelectedRecovery -> the production recovery worker
// -> Engine.Recover -> the real RecoveryDriver. There is no second
// recovery implementation and no exported trigger variable.
func (m *Manager) handleTestControlMethod(_ context.Context, request Request) (handled bool, err error) {
	if request.Method != testControlMethodName {
		return false, nil
	}
	if request.Role == "" {
		return true, fmt.Errorf("%s requires a role", testControlMethodName)
	}
	if !m.requestManagedSelectedRecovery(request.Role, "test-trigger") {
		return true, fmt.Errorf("managed recovery was not scheduled for role %q", request.Role)
	}
	return true, nil
}

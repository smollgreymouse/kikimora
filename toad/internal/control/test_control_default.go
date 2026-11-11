//go:build !kikimora_test_failpoints

package control

import "context"

// handleTestControlMethod is the release-build stub of the tagged control
// surface: it never handles anything, so every request falls through to the
// production method switch and an unknown test method returns the normal
// unsupported-method error. The method name itself must not appear in a
// release binary (R3 §5.1/R7 §9.3); only this generic helper exists.
func (m *Manager) handleTestControlMethod(_ context.Context, _ Request) (handled bool, err error) {
	return false, nil
}

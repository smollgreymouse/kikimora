//go:build kikimora_test_failpoints

package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/netstate"
)

// barrierRecordingDriver records every RecoveryDriver call including
// WithdrawSelected, which the shared engine_test fake deliberately skips.
type barrierRecordingDriver struct {
	calls []RecoveryStep
}

func (d *barrierRecordingDriver) record(step RecoveryStep) error {
	d.calls = append(d.calls, step)
	return nil
}

func (d *barrierRecordingDriver) ObserveRoutes(_ context.Context, _ string) error {
	return d.record(RecoveryObserveRoutes)
}
func (d *barrierRecordingDriver) Park(_ context.Context, _ string) error {
	return d.record(RecoveryPark)
}
func (d *barrierRecordingDriver) WithdrawSelected(_ context.Context, _ string) error {
	return d.record(RecoveryWithdrawSelected)
}
func (d *barrierRecordingDriver) Withdraw(_ context.Context, _ string) error {
	return d.record(RecoveryWithdraw)
}
func (d *barrierRecordingDriver) Quiesce(_ context.Context, _ string) error {
	return d.record(RecoveryQuiesce)
}
func (d *barrierRecordingDriver) ApplyEndpoint(_ context.Context, _ string) error {
	return d.record(RecoveryApplyEndpoint)
}
func (d *barrierRecordingDriver) Rebind(_ context.Context, _ string) error {
	return d.record(RecoveryRebind)
}
func (d *barrierRecordingDriver) StartTransport(_ context.Context, _ string) error {
	return d.record(RecoveryStartTransport)
}
func (d *barrierRecordingDriver) Validate(_ context.Context, _ string) error {
	return d.record(RecoveryValidate)
}
func (d *barrierRecordingDriver) Publish(_ context.Context, _ string) error {
	return d.record(RecoveryPublish)
}
func (d *barrierRecordingDriver) ResyncLeshy(_ context.Context, _ string) error {
	return d.record(RecoveryResyncLeshy)
}
func (d *barrierRecordingDriver) ObserveRestoration(_ context.Context, _ string) error {
	return d.record(RecoveryObserveRestore)
}
func (d *barrierRecordingDriver) FreezeSelected(_ context.Context, _ string) error {
	return d.record(RecoveryFreezeSelected)
}
func (d *barrierRecordingDriver) RestoreSelected(_ context.Context, _ string) error {
	return d.record(RecoveryRestoreSelected)
}
func (d *barrierRecordingDriver) UnfreezeSelected(_ context.Context, _ string) error {
	return d.record(RecoveryUnfreezeSelected)
}
func (d *barrierRecordingDriver) ReleaseResources(_ context.Context, _ string) error {
	return d.record(releaseResourcesStep)
}

// waitForBarrier polls for the .waiting marker so a test never sleeps a
// fixed guess; it fails the test on timeout instead.
func waitForBarrier(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatalf("probe %s: %v", path, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the .waiting marker never appeared at %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func barrierEngine(t *testing.T, driver RecoveryDriver) Engine {
	t.Helper()
	c := NewController([]RoleSpec{{ID: "one"}})
	if err := c.SetRoleDesired(context.Background(), "one", true); err != nil {
		t.Fatal(err)
	}
	c.SetUnderlay(netstate.Snapshot{IPv4: &netstate.Path{Family: 4, IfIndex: 2, Interface: "eth0"}}, netstate.ChangeInitial)
	return Engine{
		Controller:            c,
		Driver:                driver,
		ManagedSelectedRoutes: func(string) bool { return true },
	}
}

func TestRecoveryBarrierNonMatchingStepDoesNotWait(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KK_RECOVERY_BARRIER_STEP", "withdraw-selected")
	t.Setenv("KK_RECOVERY_BARRIER_DIR", dir)
	// The staged step is withdraw-selected; the call names park, so the
	// barrier must return immediately without creating any marker.
	done := make(chan error, 1)
	go func() { done <- processRecoveryBarrier(context.Background(), "one", RecoveryPark) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("non-matching step barrier: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the barrier waited on a non-matching step")
	}
	if _, err := os.Stat(filepath.Join(dir, "one.park.waiting")); !os.IsNotExist(err) {
		t.Fatal("a .waiting marker was created for a non-matching step")
	}
}

func TestRecoveryBarrierNonMatchingRoleDoesNotWait(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KK_RECOVERY_BARRIER_STEP", "park")
	t.Setenv("KK_RECOVERY_BARRIER_ROLE", "other")
	t.Setenv("KK_RECOVERY_BARRIER_DIR", dir)
	done := make(chan error, 1)
	go func() { done <- processRecoveryBarrier(context.Background(), "one", RecoveryPark) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("non-matching role barrier: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the barrier waited on a non-matching role")
	}
}

func TestRecoveryBarrierMatchingStepCreatesWaitingAndContinueReleases(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KK_RECOVERY_BARRIER_STEP", "park")
	t.Setenv("KK_RECOVERY_BARRIER_ROLE", "one")
	t.Setenv("KK_RECOVERY_BARRIER_DIR", dir)
	waiting := filepath.Join(dir, "one.park.waiting")
	cont := filepath.Join(dir, "one.park.continue")

	done := make(chan error, 1)
	go func() { done <- processRecoveryBarrier(context.Background(), "one", RecoveryPark) }()
	waitForBarrier(t, waiting)
	if _, err := os.Stat(cont); !os.IsNotExist(err) {
		t.Fatal(".continue appeared before the test staged it")
	}
	// Continue: both markers must be gone after the release.
	if err := os.WriteFile(cont, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("continue did not release the barrier: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal(".continue did not release the barrier")
	}
	if _, err := os.Stat(cont); !os.IsNotExist(err) {
		t.Fatal(".continue survived the release")
	}
	if _, err := os.Stat(waiting); !os.IsNotExist(err) {
		t.Fatal(".waiting survived the release")
	}
}

func TestRecoveryBarrierMissingDirIsAnError(t *testing.T) {
	t.Setenv("KK_RECOVERY_BARRIER_STEP", "park")
	t.Setenv("KK_RECOVERY_BARRIER_ROLE", "")
	t.Setenv("KK_RECOVERY_BARRIER_DIR", "")
	if err := processRecoveryBarrier(context.Background(), "one", RecoveryPark); err == nil {
		t.Fatal("a matching step without KK_RECOVERY_BARRIER_DIR must be an error")
	}
}

func TestRecoveryBarrierTimeoutReturnsErrorAndKeepsWaitingMarker(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KK_RECOVERY_BARRIER_STEP", "park")
	t.Setenv("KK_RECOVERY_BARRIER_ROLE", "one")
	t.Setenv("KK_RECOVERY_BARRIER_DIR", dir)
	waiting := filepath.Join(dir, "one.park.waiting")

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	err := processRecoveryBarrier(ctx, "one", RecoveryPark)
	if err == nil {
		t.Fatal("a timed-out barrier must return an error")
	}
	// The .waiting marker stays for diagnostics.
	if _, statErr := os.Stat(waiting); statErr != nil {
		t.Fatalf("the .waiting marker must survive the timeout: %v", statErr)
	}
}

func TestRecoveryBarrierDriverFailureDoesNotCreateWaiting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KK_RECOVERY_BARRIER_STEP", "freeze-selected")
	t.Setenv("KK_RECOVERY_BARRIER_ROLE", "one")
	t.Setenv("KK_RECOVERY_BARRIER_DIR", dir)

	driver := &barrierRecordingDriver{}
	// A driver failure at the FIRST step must never reach the barrier.
	engine := barrierEngine(t, &failingDriver{driver: driver, fail: RecoveryFreezeSelected})
	if err := engine.Recover(context.Background(), "one", 1, 1); err == nil {
		t.Fatal("the driver failure was swallowed")
	}
	if _, err := os.Stat(filepath.Join(dir, "one.freeze-selected.waiting")); !os.IsNotExist(err) {
		t.Fatal("a driver failure created a .waiting marker")
	}
}

// failingDriver delegates every call and fails exactly one step before the
// delegate records it.
type failingDriver struct {
	driver *barrierRecordingDriver
	fail   RecoveryStep
}

func (d *failingDriver) call(ctx context.Context, role string, step RecoveryStep) error {
	if step == d.fail {
		return errors.New("driver failed at " + string(step))
	}
	return d.driver.record(step)
}

func (d *failingDriver) ObserveRoutes(ctx context.Context, role string) error {
	return d.call(ctx, role, RecoveryObserveRoutes)
}
func (d *failingDriver) Park(ctx context.Context, role string) error {
	return d.call(ctx, role, RecoveryPark)
}
func (d *failingDriver) WithdrawSelected(ctx context.Context, role string) error {
	return d.call(ctx, role, RecoveryWithdrawSelected)
}
func (d *failingDriver) Withdraw(ctx context.Context, role string) error {
	return d.call(ctx, role, RecoveryWithdraw)
}
func (d *failingDriver) Quiesce(ctx context.Context, role string) error {
	return d.call(ctx, role, RecoveryQuiesce)
}
func (d *failingDriver) ApplyEndpoint(ctx context.Context, role string) error {
	return d.call(ctx, role, RecoveryApplyEndpoint)
}
func (d *failingDriver) Rebind(ctx context.Context, role string) error {
	return d.call(ctx, role, RecoveryRebind)
}
func (d *failingDriver) StartTransport(ctx context.Context, role string) error {
	return d.call(ctx, role, RecoveryStartTransport)
}
func (d *failingDriver) Validate(ctx context.Context, role string) error {
	return d.call(ctx, role, RecoveryValidate)
}
func (d *failingDriver) Publish(ctx context.Context, role string) error {
	return d.call(ctx, role, RecoveryPublish)
}
func (d *failingDriver) ResyncLeshy(ctx context.Context, role string) error {
	return d.call(ctx, role, RecoveryResyncLeshy)
}
func (d *failingDriver) ObserveRestoration(ctx context.Context, role string) error {
	return d.call(ctx, role, RecoveryObserveRestore)
}
func (d *failingDriver) FreezeSelected(ctx context.Context, role string) error {
	return d.call(ctx, role, RecoveryFreezeSelected)
}
func (d *failingDriver) RestoreSelected(ctx context.Context, role string) error {
	return d.call(ctx, role, RecoveryRestoreSelected)
}
func (d *failingDriver) UnfreezeSelected(ctx context.Context, role string) error {
	return d.call(ctx, role, RecoveryUnfreezeSelected)
}
func (d *failingDriver) ReleaseResources(ctx context.Context, role string) error {
	return d.call(ctx, role, releaseResourcesStep)
}

func TestRecoveryBarrierErrorPreventsNextRecoveryStep(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KK_RECOVERY_BARRIER_STEP", "park")
	t.Setenv("KK_RECOVERY_BARRIER_ROLE", "one")
	t.Setenv("KK_RECOVERY_BARRIER_DIR", dir)

	driver := &barrierRecordingDriver{}
	engine := barrierEngine(t, driver)
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	if err := engine.Recover(ctx, "one", 1, 1); err == nil {
		t.Fatal("the barrier timeout must fail the recovery")
	}
	// Park completed (the barrier is after the step), but Withdraw and
	// everything after it never started.
	for _, step := range driver.calls {
		if step == RecoveryWithdraw {
			t.Fatalf("recovery advanced past a failed barrier: %v", driver.calls)
		}
	}
	want := []RecoveryStep{RecoveryFreezeSelected, RecoveryObserveRoutes, RecoveryPark}
	if !reflect.DeepEqual(driver.calls, want) {
		t.Fatalf("calls = %v, want %v", driver.calls, want)
	}
}

func TestRecoveryWithdrawSelectedMarkerOrdering(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KK_RECOVERY_BARRIER_STEP", "withdraw-selected")
	t.Setenv("KK_RECOVERY_BARRIER_ROLE", "one")
	t.Setenv("KK_RECOVERY_BARRIER_DIR", dir)
	waiting := filepath.Join(dir, "one.withdraw-selected.waiting")

	driver := &barrierRecordingDriver{}
	engine := barrierEngine(t, driver)
	type result struct {
		err error
	}
	done := make(chan result, 1)
	go func() { done <- result{engine.Recover(context.Background(), "one", 1, 1)} }()

	// The withdraw-selected marker must mean exactly: FreezeSelected,
	// ObserveRoutes, Park and WithdrawSelected complete, Withdraw not started.
	waitForBarrier(t, waiting)
	want := []RecoveryStep{RecoveryFreezeSelected, RecoveryObserveRoutes, RecoveryPark, RecoveryWithdrawSelected}
	if !reflect.DeepEqual(driver.calls, want) {
		t.Fatalf("calls at the barrier = %v, want %v", driver.calls, want)
	}

	if err := os.WriteFile(filepath.Join(dir, "one.withdraw-selected.continue"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("recovery after the barrier: %v", r.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the recovery never completed after the continue")
	}
	// Withdraw ran strictly after the released withdraw-selected barrier.
	found := false
	for _, step := range driver.calls {
		if step == RecoveryWithdrawSelected {
			found = true
		}
		if step == RecoveryWithdraw && !found {
			t.Fatalf("withdraw ran before the withdraw-selected barrier: %v", driver.calls)
		}
	}
}

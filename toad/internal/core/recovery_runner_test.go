package core

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestRecoverySequenceForTransportActions(t *testing.T) {
	tests := []struct {
		name   string
		action RecoveryAction
		want   []RecoveryStep
	}{
		{
			name:   "rebind",
			action: ActionRebind,
			want: []RecoveryStep{
				RecoveryObserveRoutes, RecoveryPark, RecoveryWithdraw, RecoveryQuiesce,
				RecoveryApplyEndpoint, RecoveryRebind, RecoveryValidate, RecoveryPublish,
				RecoveryResyncLeshy, RecoveryObserveRestore,
			},
		},
		{
			name:   "restart transport",
			action: ActionRestartTransport,
			want: []RecoveryStep{
				RecoveryObserveRoutes, RecoveryPark, RecoveryWithdraw, RecoveryQuiesce,
				RecoveryApplyEndpoint, RecoveryStartTransport, RecoveryValidate, RecoveryPublish,
				RecoveryResyncLeshy, RecoveryObserveRestore,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := RecoverySequenceForAction(tc.action); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("sequence = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRecoveryRunnerUsesFailClosedOrder(t *testing.T) {
	var got []RecoveryStep
	role := &RoleRuntime{}
	if err := RunRecovery(context.Background(), role, 4, 9, func(_ context.Context, step RecoveryStep) error {
		got = append(got, step)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := RecoverySequence()
	if !reflect.DeepEqual(got, want) || role.Recovery.Step != "" || role.Recovery.Attempt != 1 {
		t.Fatalf("bad recovery sequence: got=%v want=%v state=%#v", got, want, role.Recovery)
	}
}

func TestRecoveryRunnerLeavesFailedStepRecorded(t *testing.T) {
	role := &RoleRuntime{}
	wantErr := errors.New("Leshy unavailable")
	err := RunRecovery(context.Background(), role, 8, 3, func(_ context.Context, step RecoveryStep) error {
		if step == RecoveryPublish {
			return wantErr
		}
		return nil
	})
	if !errors.Is(err, wantErr) || role.Recovery.Step != RecoveryPublish || role.Recovery.LastError != wantErr.Error() || role.Recovery.Operation != 8 || role.Recovery.Epoch != 3 {
		t.Fatalf("failed recovery was not reconstructible: %#v", role.Recovery)
	}
}

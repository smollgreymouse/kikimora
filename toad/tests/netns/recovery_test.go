package netns

import (
	"context"
	"github.com/smollgreymouse/kikimora/toad/internal/core"
	"testing"
)

func TestRecoveryCompletionCannotCrossOperation(t *testing.T) {
	c := core.NewController([]core.RoleSpec{{ID: "primary"}})
	if err := c.SetRoleDesired(context.Background(), "primary", true); err != nil {
		t.Fatal(err)
	}
	s := c.Snapshot().Roles["primary"]
	if c.Complete("primary", s.Operation+1, 0, core.OperationResult{}) {
		t.Fatal("old recovery committed")
	}
}

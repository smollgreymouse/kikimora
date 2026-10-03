//go:build linux

package control

import (
	"os"
	"testing"
)

func TestPeerSupplementaryGroupsReadsProcStatus(t *testing.T) {
	groups, err := peerSupplementaryGroups(int32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	if groups == nil {
		t.Fatal("peer supplementary groups must not be nil")
	}
}

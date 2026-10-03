//go:build linux

package platform

import (
	"errors"
	"testing"

	"github.com/smollgreymouse/kikimora/toad/internal/interfaceinfo"
)

func TestVerifyRepairIdentityRejectsReplacementIfIndex(t *testing.T) {
	err := verifyRepairIdentity("kk-awg0", 8, interfaceinfo.Expectation{IfIndex: 7})
	var mismatch *ManagedInterfaceIdentityError
	if !errors.As(err, &mismatch) {
		t.Fatalf("error = %v, want ManagedInterfaceIdentityError", err)
	}
	if mismatch.ExpectedIfIndex != 7 || mismatch.ActualIfIndex != 8 || mismatch.Name != "kk-awg0" {
		t.Fatalf("unexpected mismatch: %#v", mismatch)
	}
}

func TestVerifyRepairIdentityAllowsUnknownOrMatchingIdentity(t *testing.T) {
	for _, expected := range []interfaceinfo.Expectation{
		{},
		{IfIndex: 7},
	} {
		if err := verifyRepairIdentity("kk-awg0", 7, expected); err != nil {
			t.Fatalf("expected=%#v: %v", expected, err)
		}
	}
}

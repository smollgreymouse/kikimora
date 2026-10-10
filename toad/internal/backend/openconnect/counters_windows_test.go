//go:build windows

package openconnect

import (
	"errors"
	"testing"

	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

func TestReadInterfaceCountersReturnsZeroOnFailure(t *testing.T) {
	previous := ifTable2Ex
	ifTable2Ex = func(_ winipcfg.MibIfEntryLevel) ([]winipcfg.MibIfRow2, error) {
		return nil, assertAnError
	}
	t.Cleanup(func() { ifTable2Ex = previous })

	rx, tx := readInterfaceCounters("nope")
	if rx != 0 || tx != 0 {
		t.Fatalf("counters = %d/%d, want 0/0", rx, tx)
	}
}

func TestReadInterfaceCountersReturnsZeroWhenNotFound(t *testing.T) {
	previous := ifTable2Ex
	ifTable2Ex = func(_ winipcfg.MibIfEntryLevel) ([]winipcfg.MibIfRow2, error) {
		return []winipcfg.MibIfRow2{{}}, nil
	}
	t.Cleanup(func() { ifTable2Ex = previous })

	rx, tx := readInterfaceCounters("nope")
	if rx != 0 || tx != 0 {
		t.Fatalf("counters = %d/%d, want 0/0", rx, tx)
	}
}

func TestReadInterfaceCountersIteratesTableWithoutPanic(t *testing.T) {
	previous := ifTable2Ex
	ifTable2Ex = func(_ winipcfg.MibIfEntryLevel) ([]winipcfg.MibIfRow2, error) {
		return []winipcfg.MibIfRow2{
			{},
			{InOctets: 100, OutOctets: 200},
			{InOctets: 300, OutOctets: 400},
		}, nil
	}
	t.Cleanup(func() { ifTable2Ex = previous })

	// The mock has no aliases, so any name returns 0/0 but must not panic.
	rx, tx := readInterfaceCounters("test-if")
	if rx != 0 || tx != 0 {
		t.Fatalf("counters = %d/%d, want 0/0 for unknown alias", rx, tx)
	}
}

var assertAnError = errors.New("assert an error")

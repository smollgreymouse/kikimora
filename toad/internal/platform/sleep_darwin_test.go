//go:build darwin

package platform

import "testing"

func TestParseDarwinPowerEvent(t *testing.T) {
	tests := []struct {
		line      string
		preparing bool
		ok        bool
	}{
		{line: "powerd: Entering Sleep state", preparing: true, ok: true},
		{line: "powerd: Wake from Normal Sleep", preparing: false, ok: true},
		{line: "powerd: thermal policy changed", ok: false},
	}
	for _, test := range tests {
		event, ok := parseDarwinPowerEvent(test.line)
		if ok != test.ok || (ok && event.Preparing != test.preparing) {
			t.Fatalf("parseDarwinPowerEvent(%q) = %#v, %v; want preparing=%v, %v", test.line, event, ok, test.preparing, test.ok)
		}
	}
}

//go:build linux

package logind

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestPrepareForSleepEvent(t *testing.T) {
	for _, tc := range []struct {
		name string
		sig  *dbus.Signal
		ok   bool
		want bool
	}{
		{
			name: "suspend",
			sig:  &dbus.Signal{Path: login1Path, Name: login1Interface + ".PrepareForSleep", Body: []any{true}},
			ok:   true,
			want: true,
		},
		{
			name: "resume",
			sig:  &dbus.Signal{Path: login1Path, Name: login1Interface + ".PrepareForSleep", Body: []any{false}},
			ok:   true,
		},
		{
			name: "wrong member",
			sig:  &dbus.Signal{Path: login1Path, Name: login1Interface + ".Other", Body: []any{true}},
		},
		{
			name: "wrong body",
			sig:  &dbus.Signal{Path: login1Path, Name: login1Interface + ".PrepareForSleep", Body: []any{"true"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := prepareForSleepEvent(tc.sig)
			if ok != tc.ok || (ok && got.Preparing != tc.want) {
				t.Fatalf("event=%+v ok=%v want preparing=%v ok=%v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

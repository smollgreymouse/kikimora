//go:build linux

package networkmanager

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestOwnershipInvalidationSignal(t *testing.T) {
	tests := []struct {
		name string
		sig  *dbus.Signal
		want bool
	}{
		{
			name: "device added",
			sig: &dbus.Signal{
				Path: managerPath,
				Name: managerIface + ".DeviceAdded",
				Body: []any{dbus.ObjectPath("/org/freedesktop/NetworkManager/Devices/7")},
			},
			want: true,
		},
		{
			name: "networkmanager owner changed",
			sig: &dbus.Signal{
				Path: dbus.ObjectPath("/org/freedesktop/DBus"),
				Name: "org.freedesktop.DBus.NameOwnerChanged",
				Body: []any{busName, ":1.20", ":1.21"},
			},
			want: true,
		},
		{
			name: "unrelated owner changed",
			sig: &dbus.Signal{
				Path: dbus.ObjectPath("/org/freedesktop/DBus"),
				Name: "org.freedesktop.DBus.NameOwnerChanged",
				Body: []any{"org.example.Other", ":1.20", ":1.21"},
			},
		},
		{
			name: "wrong signal",
			sig:  &dbus.Signal{Path: managerPath, Name: managerIface + ".StateChanged"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ownershipInvalidationSignal(tc.sig); got != tc.want {
				t.Fatalf("ownershipInvalidationSignal()=%v want=%v signal=%#v", got, tc.want, tc.sig)
			}
		})
	}
}

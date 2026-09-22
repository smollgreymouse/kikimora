//go:build linux

package networkmanager

import (
	"context"
	"fmt"

	"github.com/godbus/dbus/v5"
)

const (
	busName             = "org.freedesktop.NetworkManager"
	managerPath         = dbus.ObjectPath("/org/freedesktop/NetworkManager")
	managerIface        = "org.freedesktop.NetworkManager"
	deviceIface         = "org.freedesktop.NetworkManager.Device"
	propertiesIface     = "org.freedesktop.DBus.Properties"
	deviceManagedNo     = uint32(0)
	deviceManagedRuntime = uint32(1)
)

type DeviceState struct {
	Present bool
	Managed bool
}

type Manager struct{}

func (Manager) EnsureUnmanaged(ctx context.Context, name string) (DeviceState, error) {
	if name == "" {
		return DeviceState{}, fmt.Errorf("NetworkManager interface name is empty")
	}
	conn, err := dbus.SystemBusPrivate()
	if err != nil {
		return DeviceState{}, fmt.Errorf("open system D-Bus: %w", err)
	}
	defer conn.Close()
	if err := conn.Auth(nil); err != nil {
		return DeviceState{}, fmt.Errorf("authenticate system D-Bus: %w", err)
	}
	if err := conn.Hello(); err != nil {
		return DeviceState{}, fmt.Errorf("hello system D-Bus: %w", err)
	}

	var owned bool
	if err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.NameHasOwner", 0, busName).Store(&owned); err != nil {
		return DeviceState{}, fmt.Errorf("query NetworkManager owner: %w", err)
	}
	if !owned {
		return DeviceState{}, nil
	}

	manager := conn.Object(busName, managerPath)
	var path dbus.ObjectPath
	if err := manager.CallWithContext(ctx, managerIface+".GetDeviceByIpIface", 0, name).Store(&path); err != nil {
		return DeviceState{Present: false}, nil
	}
	device := conn.Object(busName, path)
	managed, err := readManaged(device)
	if err != nil {
		return DeviceState{Present: true}, err
	}
	if !managed {
		return DeviceState{Present: true, Managed: false}, nil
	}

	// NetworkManager >= 1.58 exposes Device.SetManaged(managed, flags).
	// Use the runtime flag because the packaged keyfile rule is the persistent
	// authority. Older NetworkManager versions fall back to the writable
	// Managed property.
	modernErr := device.CallWithContext(
		ctx,
		deviceIface+".SetManaged",
		0,
		deviceManagedNo,
		deviceManagedRuntime,
	).Err
	if modernErr != nil {
		fallback := device.CallWithContext(ctx, propertiesIface+".Set", 0, deviceIface, "Managed", dbus.MakeVariant(false))
		if fallback.Err != nil {
			return DeviceState{Present: true, Managed: true}, fmt.Errorf(
				"mark %s unmanaged in NetworkManager: SetManaged: %v; property fallback: %w",
				name, modernErr, fallback.Err,
			)
		}
	}
	managed, err = readManaged(device)
	if err != nil {
		return DeviceState{Present: true}, err
	}
	if managed {
		return DeviceState{Present: true, Managed: true}, fmt.Errorf("NetworkManager retained management of %s", name)
	}
	return DeviceState{Present: true, Managed: false}, nil
}

func readManaged(device dbus.BusObject) (bool, error) {
	var variant dbus.Variant
	if err := device.Call(propertiesIface+".Get", 0, deviceIface, "Managed").Store(&variant); err != nil {
		return false, fmt.Errorf("read NetworkManager Device.Managed: %w", err)
	}
	managed, ok := variant.Value().(bool)
	if !ok {
		return false, fmt.Errorf("NetworkManager Device.Managed has unexpected type %T", variant.Value())
	}
	return managed, nil
}

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


func (Manager) Watch(ctx context.Context, out chan<- struct{}) error {
	conn, err := dbus.SystemBusPrivate()
	if err != nil {
		return fmt.Errorf("open system D-Bus: %w", err)
	}
	defer conn.Close()
	if err := conn.Auth(nil); err != nil {
		return fmt.Errorf("authenticate system D-Bus: %w", err)
	}
	if err := conn.Hello(); err != nil {
		return fmt.Errorf("hello system D-Bus: %w", err)
	}
	if err := conn.AddMatchSignal(
		dbus.WithMatchSender(busName),
		dbus.WithMatchObjectPath(managerPath),
		dbus.WithMatchInterface(managerIface),
		dbus.WithMatchMember("DeviceAdded"),
	); err != nil {
		return fmt.Errorf("subscribe NetworkManager DeviceAdded: %w", err)
	}
	if err := conn.AddMatchSignal(
		dbus.WithMatchSender("org.freedesktop.DBus"),
		dbus.WithMatchObjectPath(dbus.ObjectPath("/org/freedesktop/DBus")),
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
	); err != nil {
		return fmt.Errorf("subscribe D-Bus NameOwnerChanged: %w", err)
	}

	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case signal, ok := <-signals:
			if !ok {
				return fmt.Errorf("system D-Bus signal channel closed")
			}
			if !ownershipInvalidationSignal(signal) {
				continue
			}
			select {
			case out <- struct{}{}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func ownershipInvalidationSignal(signal *dbus.Signal) bool {
	if signal == nil {
		return false
	}
	if signal.Path == managerPath && signal.Name == managerIface+".DeviceAdded" {
		return true
	}
	if signal.Path != dbus.ObjectPath("/org/freedesktop/DBus") ||
		signal.Name != "org.freedesktop.DBus.NameOwnerChanged" ||
		len(signal.Body) != 3 {
		return false
	}
	name, ok := signal.Body[0].(string)
	return ok && name == busName
}

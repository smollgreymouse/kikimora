package leshy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/smollgreymouse/kikimora/toad/internal/fsutil"
)

type RolePublication struct {
	Role      string
	Zone      string
	Interface string
}
type Bridge interface {
	Publish(context.Context, RolePublication) error
	Withdraw(context.Context, string) error
	Resync(context.Context, string) error
}
type FileBridge struct{ Dir string }

func (b FileBridge) path(role string) string { return filepath.Join(b.Dir, role+".dev") }
func (b FileBridge) Publish(_ context.Context, p RolePublication) error {
	if p.Role == "" || p.Interface == "" {
		return fmt.Errorf("invalid Leshy publication")
	}
	if err := os.MkdirAll(b.Dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(b.Dir, ".dev-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.WriteString(p.Interface + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, b.path(p.Role)); err != nil {
		return err
	}
	return syncDir(b.Dir)
}
func (b FileBridge) Withdraw(_ context.Context, role string) error {
	if role == "" {
		return nil
	}
	err := os.Remove(b.path(role))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return syncDir(b.Dir)
}
func (b FileBridge) Resync(ctx context.Context, role string) error {
	if role == "" {
		return nil
	}
	data, err := os.ReadFile(b.path(role))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	iface := strings.TrimSpace(string(data))
	if iface == "" {
		return fmt.Errorf("empty Leshy publication for %q", role)
	}
	return b.Publish(ctx, RolePublication{Role: role, Interface: iface})
}

func syncDir(path string) error {
	return fsutil.SyncDir(path)
}

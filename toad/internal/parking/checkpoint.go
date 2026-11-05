package parking

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/smollgreymouse/kikimora/toad/internal/fsutil"
)

type Checkpoint struct {
	BootID         string       `json:"boot_id"`
	CoreInstanceID string       `json:"core_instance_id"`
	Role           string       `json:"role"`
	Interface      string       `json:"interface"`
	IfIndex        int          `json:"ifindex"`
	Baseline       []OwnedRoute `json:"baseline"`
	Observed       []OwnedRoute `json:"observed"`
	Parked         []OwnedRoute `json:"parked"`
}

func WriteCheckpoint(path string, value Checkpoint) error {
	return fsutil.WriteAtomicJSON(path, value, 0o600)
}
func ReadCheckpoint(path string) (Checkpoint, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Checkpoint{}, err
	}
	var c Checkpoint
	if err := json.Unmarshal(b, &c); err != nil {
		return Checkpoint{}, fmt.Errorf("decode parking checkpoint: %w", err)
	}
	return c, nil
}

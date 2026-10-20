package supervisor

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ToadLogDir is the environment variable that sends each toad's stdout and stderr to its own
// file. Without it a toad inherits the core's streams, which a service does not keep.
const ToadLogDir = "KIKIMORA_TOAD_LOG_DIR"

// toadLogWriter opens <dir>/toad-<binary>.log for appending. A nil writer means "keep the
// inherited stream", which is what a foreground core wants.
func toadLogWriter(binary string) io.Writer {
	dir := strings.TrimSpace(os.Getenv(ToadLogDir))
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	name := strings.TrimSuffix(filepath.Base(binary), filepath.Ext(binary))
	file, err := os.OpenFile(filepath.Join(dir, "toad-"+name+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil
	}
	return file
}

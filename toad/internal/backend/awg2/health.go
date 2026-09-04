package awg2

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/smollgreymouse/kikimora/toad/internal/backend"
)

const recentHandshakeWindow = 3 * time.Minute

func parseHealthUAPI(raw string, now time.Time) (backend.Health, error) {
	health := backend.Health{State: "connecting", Reason: "awaiting AWG2 handshake"}
	var handshakeSec int64
	var handshakeNsec int64

	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "last_handshake_time_sec":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return backend.Health{}, fmt.Errorf("parse last_handshake_time_sec: %w", err)
			}
			handshakeSec = parsed
		case "last_handshake_time_nsec":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return backend.Health{}, fmt.Errorf("parse last_handshake_time_nsec: %w", err)
			}
			handshakeNsec = parsed
		case "rx_bytes":
			parsed, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return backend.Health{}, fmt.Errorf("parse rx_bytes: %w", err)
			}
			health.RXBytes = parsed
		case "tx_bytes":
			parsed, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return backend.Health{}, fmt.Errorf("parse tx_bytes: %w", err)
			}
			health.TXBytes = parsed
		case "endpoint":
			health.Endpoint = value
		}
	}
	if err := scanner.Err(); err != nil {
		return backend.Health{}, fmt.Errorf("scan AWG2 health: %w", err)
	}

	if handshakeSec <= 0 {
		return health, nil
	}

	handshakeAt := time.Unix(handshakeSec, handshakeNsec)
	age := now.Sub(handshakeAt)
	if age < 0 {
		age = 0
	}
	health.LastHandshakeAge = &age
	if age <= recentHandshakeWindow {
		health.State = "online"
		health.Reason = "recent AWG2 handshake"
		health.Connected = true
	} else {
		health.State = "reconnecting"
		health.Reason = "AWG2 handshake is stale"
	}
	return health, nil
}

package core

import (
	"github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	"github.com/smollgreymouse/kikimora/toad/internal/leshy"
	"github.com/smollgreymouse/kikimora/toad/internal/parking"
	"github.com/smollgreymouse/kikimora/toad/internal/toadctl"
	"time"
)

type RoleState string

const (
	RoleStopped            RoleState = "Stopped"
	RoleWaitingForUnderlay RoleState = "WaitingForUnderlay"
	RolePreparingEndpoint  RoleState = "PreparingEndpoint"
	RoleStarting           RoleState = "Starting"
	RoleValidating         RoleState = "Validating"
	RoleReady              RoleState = "Ready"
	RoleRecovering         RoleState = "Recovering"
	RoleStopping           RoleState = "Stopping"
	RoleFailed             RoleState = "Failed"
)

type RecoveryState struct {
	Step        RecoveryStep `json:"step,omitempty"`
	Operation   uint64       `json:"operation,omitempty"`
	Epoch       uint64       `json:"epoch,omitempty"`
	Attempt     int          `json:"attempt,omitempty"`
	LastError   string       `json:"last_error,omitempty"`
	NextRetryAt time.Time    `json:"next_retry_at,omitempty"`
}
type RoleSpec struct {
	ID        string
	Protocol  string
	Interface string
	LeshyZone string
}
type RoleRuntime struct {
	Desired        bool                     `json:"desired"`
	State          RoleState                `json:"state"`
	Reason         string                   `json:"reason,omitempty"`
	Operation      uint64                   `json:"operation"`
	ToadGeneration uint64                   `json:"toad_generation"`
	ValidatedEpoch uint64                   `json:"validated_underlay_epoch"`
	Toad           toadctl.Snapshot         `json:"toad"`
	Endpoint       endpoint.State           `json:"endpoint"`
	Publication    leshy.PublicationState   `json:"publication"`
	Parking        parking.State            `json:"parking"`
	Recovery       RecoveryState            `json:"recovery"`
	Validation     toadctl.ValidationResult `json:"validation"`
	LastError      string                   `json:"last_error,omitempty"`
}

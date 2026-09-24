#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Deterministic regressions distilled from failures first observed in the
# operator-privileged 07F.2 kernel/protocol suite.
#
# Rule: every new privileged blocker must gain a focused non-privileged
# regression here (or in a package already selected here) before another
# privileged run is requested.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../../.." && pwd)"
TOAD_DIR="$REPO_ROOT/toad"

cd "$TOAD_DIR"

echo "==> privileged-derived regression model: AWG health vs structural readiness"
go test ./internal/backend/awg2 ./internal/toadruntime -run   'TestValidationRequiresRecentHandshake|TestAWGRouteReadyIsStructuralNotPeerHealth|TestHealthLoopDoesNotRepairStructurallyReadyAWGForStaleHandshake|TestValidateRequiresStructuralRouteTarget|TestStructuralReadinessRequiresAllConfiguredAddresses'   -count=1

echo "==> privileged-derived regression model: stable recovery handoff"
go test ./internal/core ./internal/control -run   'TestRecoveryDecisionUsesCapabilitiesForStaleUnderlay|TestRecoveryDecisionStructuralRouteLossOverridesRebind|TestBeginToadGenerationInvalidatesCurrentValidation|TestValidationPendingCommitsRecoveringCurrentResult|TestPendingValidationStaysRecoverableAndRejectsStaleToken|TestRouteReadyLossInvalidatesValidationImmediately|TestRecoverySequenceForTransportActions|TestRecoveryRunnerUsesFailClosedOrder|TestStableTransportPendingValidationDoesNotRestartTwice|TestRouteReadyLossSchedulesSingleAutomaticRecovery|TestAsyncFullRestartHandoff|TestReplacementStartupHandoffDoesNotReplayRecovery|TestReplacementReadyClearsFullRestartHandoff'   -count=1

echo "==> privileged-derived regression model: non-destructive protocol rebind"
go test ./internal/backend/xray ./internal/backend/openconnect ./internal/toadruntime -run   'TestBackendAdvertisesNonDestructiveRebind|TestBackendRebindHonorsCanceledContext|TestCapabilitiesMatchExecutableInterfaces|TestSupportedCapabilitiesDelegateExactlyOnce'   -count=1

echo "==> privileged-derived regression model: structural drift publication and repair"
go test ./internal/toadruntime -run   'TestHealthLoopRepairsMissingAddressWithSameIfIndex|TestHealthLoopRateLimitsIncompleteRepair|TestHealthLoopDoesNotRepairOpenConnectNegotiatedAddress|TestHealthLoopRejectsReplacementIfIndexWithoutRepair'   -count=1

echo "privileged-derived regression model: PASS"

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "../Theme"
import "../Controls"

// Diagnostics page (plan U12): exposes the canonical core truth - backend kind,
// revision, aggregate derivation inputs, underlay epoch and per-role recovery
// reasons - so a role that is not Ready can be explained from the UI alone.
Item {
    id: page
    objectName: "diagnosticsPage"
    property string titleText: "Diagnostics"

    function capture() {
        return (Core.diagnostics && Core.diagnostics.capture) ? Core.diagnostics.capture : null
    }

    function captureValue(key, fallback) {
        var c = page.capture()
        if (!c || c[key] === undefined || c[key] === null || c[key] === "") return fallback
        return String(c[key])
    }

    Component.onCompleted: Core.refreshDiagnostics()

    function summaryText() {
        var lines = []
        lines.push("backend: " + Core.backendKind)
        lines.push("platform: " + Core.platformName)
        lines.push("simulated: " + (Core.simulated ? "yes" : "no"))
        lines.push("core_state: " + Core.coreState)
        lines.push("aggregate_state: " + Core.aggregateState)
        lines.push("revision: " + Core.revision)
        lines.push("active_profile: " + Core.activeProfile)
        lines.push("leshy: " + (Core.leshySupported ? "supported" : "unsupported"))
        lines.push("underlay: " + Core.underlaySummary)
        lines.push("")
        for (var i = 0; i < Core.roles.rowCount(); ++i) {
            var r = Core.roles.get(i)
            lines.push("role " + r.label + ": state=" + r.state
                       + " route_ready=" + (r.routeReady ? "yes" : "no")
                       + " ipv4=" + (r.ipv4Configured ? (r.ipv4Ready ? "ready" : "unavailable") : "unconfigured")
                       + " ipv6=" + (r.ipv6Configured ? (r.ipv6Ready ? "ready" : "unavailable" + (r.ipv6Reason ? "(" + r.ipv6Reason + ")" : "")) : "unconfigured")
                       + " endpoint=" + r.endpointState
                       + " published=" + (r.leshyPublished ? "yes" : "no")
                       + " parking=" + (r.parkingActive ? "active" : "inactive") + "/" + r.parkedRoutes
                       + " epoch=" + r.validatedUnderlayEpoch
                       + (r.recoveryAction ? (" recovery=" + r.recoveryAction) : "")
                       + (r.nextRetryAt ? (" next_retry=" + r.nextRetryAt) : "")
                       + (r.lastError ? (" error=" + r.lastError) : ""))
            if (r.selectedRoutes && Object.keys(r.selectedRoutes).length > 0) {
                var stats = r.selectedRoutes
                var statLine = "selected_routes:"
                for (var key in stats)
                    statLine += " " + key + "=" + stats[key]
                lines.push("  " + statLine)
            }
        }
        return lines.join("\n")
    }

    function managedInterfacesText() {
        const owners = Core.diagnostics ? Core.diagnostics.managed_interfaces : null
        if (!owners || Object.keys(owners).length === 0) return "\u2014"
        const keys = Object.keys(owners)
        return keys.map(function(k) {
            const state = owners[k]
            if (state === null || typeof state !== "object")
                return k + ": " + String(state)
            const present = state.present === true ? "present" : "absent"
            const managed = state.managed === true ? "NetworkManager=managed" : "NetworkManager=unmanaged"
            return k + ": " + present + " · " + managed
        }).join("\n")
    }

    Flickable {
        anchors.fill: parent
        contentHeight: column.implicitHeight + 28
        clip: true

        ColumnLayout {
            id: column
            x: 14
            width: parent.width - 28
            spacing: KikimoraTheme.spacing

            KHeader {
                title: page.titleText
                subtitle: "Canonical state reported by the core: backends, revision, underlay epoch, publication, parking and recovery reasons."
            }

            RowLayout {
                Layout.fillWidth: true
                spacing: 10
                KIconButton {
                    objectName: "diagShowSummaryButton"
                    accessibleText: "Show diagnostics summary"
                    glyph: "{ }"
                    onClicked: summaryDialog.open()
                }
                Text {
                    Layout.fillWidth: true
                    text: "The action shows the raw diagnostics summary the UI received from the core."
                    color: KikimoraTheme.textSecondary
                    font.pixelSize: 12
                    wrapMode: Text.WordWrap
                }
            }

            KBanner {
                Layout.fillWidth: true
                visible: Core.simulated
                tone: "warning"
                text: "This build is not protecting this host: the UI is driven by a simulated core."
            }

            KCard {
                Layout.fillWidth: true
                KSectionHeader { text: "Debug capture" }
                KSwitch {
                    objectName: "diagCaptureSwitch"
                    Layout.fillWidth: true
                    label: "Collect the system journal and Kikimora, Leshy and UI logs (last 24 hours)"
                    enabled: Core.diagnosticsMutable
                    checked: page.capture() ? !!page.capture().enabled : false
                    onToggled: function(checked) { Core.setDiagnosticsCapture(checked) }
                }
                KInfoRow { label: "Folder"; value: page.captureValue("directory", "not configured") }
                KInfoRow { label: "Size"; value: page.captureValue("bytes", "0") + " bytes" }
                KInfoRow { label: "Last run"; value: page.captureValue("last_run", "never") }
                KInfoRow { label: "Last error"; value: page.captureValue("last_error", "none") }
            }

            KCard {
                Layout.fillWidth: true
                KSectionHeader { text: "Core" }
                KInfoRow { objectName: "diagBackend"; label: "Backend"; value: Core.backendKind }
                KInfoRow { objectName: "diagPlatform"; label: "Platform"; value: Core.platformName }
                KInfoRow { objectName: "diagCoreState"; label: "Core state"; value: Core.coreState }
                KInfoRow { objectName: "diagAggregateState"; label: "Aggregate state"; value: Core.aggregateState }
                KInfoRow { objectName: "diagRevision"; label: "Revision"; value: String(Core.revision) }
                KInfoRow { objectName: "diagProfile"; label: "Active profile"; value: Core.activeProfile || "\u2014" }
                KInfoRow {
                    label: "Profiles"
                    value: Core.profiles.length > 0 ? Core.profiles.join(", ") : "\u2014"
                }
                KInfoRow { label: "Leshy"; value: Core.leshySupported ? "supported" : "unsupported" }
                KInfoRow {
                    objectName: "diagLastError"
                    label: "Last command error"
                    value: Core.lastCommandError.length > 0 ? Core.lastCommandError : "\u2014"
                }
            }

            // Managed routing authority truth (roadmap §10.3): every value is
            // a core-reported backend property, nothing is reconstructed here.
            // Fingerprints are the core-provided redacted ones only.
            KCard {
                Layout.fillWidth: true
                KSectionHeader { text: "Managed routing authority" }
                KInfoRow {
                    objectName: "diagStartupReady"
                    label: "Startup ready"
                    value: Core.startupReady ? "yes" : "no"
                }
                KInfoRow {
                    objectName: "diagManagedMode"
                    label: "Managed mode"
                    value: Core.managedMode ? "yes" : "no"
                }
                KInfoRow {
                    objectName: "diagModeGeneration"
                    label: "Mode generation"
                    value: Number(Core.modeGeneration) > 0 ? String(Core.modeGeneration) : "\u2014"
                }
                KInfoRow {
                    objectName: "diagModeTransition"
                    label: "Mode transition"
                    value: Core.modeTransition.length > 0 ? Core.modeTransition : "none"
                }
                KInfoRow {
                    objectName: "diagActivationFingerprint"
                    label: "Activation fingerprint"
                    value: Core.activationIdFingerprint.length > 0 ? Core.activationIdFingerprint : "\u2014"
                }
                KInfoRow {
                    objectName: "diagTransitionFingerprint"
                    label: "Transition fingerprint"
                    value: Core.modeTransitionIdFingerprint.length > 0 ? Core.modeTransitionIdFingerprint : "\u2014"
                }
                KInfoRow {
                    objectName: "diagRouteAuthority"
                    label: "Route authority"
                    value: Core.routeAuthorityReady ? "ready" : "not ready"
                    valueColor: Core.routeAuthorityReady ? KikimoraTheme.textPrimary : KikimoraTheme.warning
                }
                KInfoRow {
                    objectName: "diagRouteAuthorityError"
                    label: "Route authority error"
                    visible: Core.routeAuthorityError.length > 0
                    value: Core.routeAuthorityError
                    valueColor: KikimoraTheme.warning
                }
                KInfoRow {
                    objectName: "diagApiCompatible"
                    label: "API compatible"
                    value: Core.apiCompatible ? "yes" : "no"
                    valueColor: Core.apiCompatible ? KikimoraTheme.textPrimary : KikimoraTheme.error
                }
                KInfoRow {
                    objectName: "diagCapabilities"
                    label: "Capabilities"
                    value: Core.coreCapabilities.length > 0 ? Core.coreCapabilities.join(", ") : "\u2014"
                }
                KInfoRow {
                    objectName: "diagCommandChannel"
                    label: "Command channel"
                    value: Core.commandChannelReady ? "ready" : "not ready"
                }
            }

            // Kernel interface ownership in the exact shape the real core
            // publishes: explicit present/managed fields per interface.
            KCard {
                Layout.fillWidth: true
                KSectionHeader { text: "Managed interfaces" }
                KInfoRow { objectName: "diagManagedInterfaces"; label: "Interfaces"; value: page.managedInterfacesText() }
            }

            KCard {
                Layout.fillWidth: true
                KSectionHeader { text: "Underlay" }
                KInfoRow { objectName: "diagUnderlaySummary"; label: "Summary"; value: Core.underlaySummary || "\u2014" }
                KInfoRow {
                    objectName: "diagUnderlayEpoch"
                    label: "Epoch"
                    value: (Core.underlay && Core.underlay.epoch !== undefined) ? String(Core.underlay.epoch) : "\u2014"
                }
                KInfoRow {
                    label: "Available"
                    value: (Core.underlay && Core.underlay.available) ? "yes" : "no"
                }
            }

            KSectionHeader { text: "Per-role truth" }

            Repeater {
                model: Core.roles
                delegate: KCard {
                    id: roleCard
                    required property string label
                    required property string protocol
                    required property string state
                    required property string stateText
                    required property bool desiredEnabled
                    required property bool routeReady
                    required property string endpointState
                    required property bool leshyPublished
                    required property bool parkingActive
                    required property int parkedRoutes
                    required property string recoveryAction
                    required property string recoveryReason
                    required property int recoveryAttempt
                    required property string nextRetryAt
                    required property string validationState
                    required property string lastError

                    Layout.fillWidth: true

                    RowLayout {
                        Layout.fillWidth: true
                        spacing: 10
                        Text {
                            Layout.fillWidth: true
                            text: roleCard.label + " \u00b7 " + roleCard.protocol
                            color: KikimoraTheme.textPrimary
                            font.pixelSize: 14
                            font.weight: Font.DemiBold
                        }
                        KStatusChip { state: roleCard.state; text: roleCard.stateText }
                    }
                    KInfoRow { label: "Desired"; value: roleCard.desiredEnabled ? "yes" : "no" }
                    KInfoRow { label: "Route target ready"; value: roleCard.routeReady ? "yes" : "no" }
                    KInfoRow { label: "Endpoint policy"; value: roleCard.endpointState || "\u2014" }
                    KInfoRow { label: "Leshy publication"; value: roleCard.leshyPublished ? "published" : "withdrawn" }
                    KInfoRow {
                        label: "Parking"
                        value: (roleCard.parkingActive ? "active" : "inactive") + " \u00b7 " + roleCard.parkedRoutes + " routes"
                    }
                    KInfoRow { label: "Recovery step"; value: roleCard.recoveryAction || "\u2014" }
                    KInfoRow { label: "Recovery reason"; value: roleCard.recoveryReason || "\u2014" }
                    KInfoRow { label: "Recovery attempt"; value: String(roleCard.recoveryAttempt) }
                    KInfoRow { label: "Validation"; value: roleCard.validationState || "\u2014" }
                    KInfoRow {
                        objectName: "diagRoleNextRetry"
                        label: "Next retry"
                        value: roleCard.nextRetryAt && roleCard.nextRetryAt.length > 0 ? roleCard.nextRetryAt : "none scheduled"
                    }
                    KInfoRow { label: "Last error"; value: roleCard.lastError || "\u2014" }
                }
            }

            KDivider { Layout.fillWidth: true }


            Item { Layout.fillHeight: true }
        }
    }

    KDialog {
        id: summaryDialog
        objectName: "diagSummaryDialog"
        title: "Diagnostics summary"
        bodyText: "Reported by " + Core.backendKind + " at revision " + Core.revision + "."
        acceptText: "Close"

        TextArea {
            objectName: "diagSummaryText"
            readOnly: true
            wrapMode: TextArea.WrapAnywhere
            text: page.summaryText()
            Accessible.name: "Diagnostics summary"
            background: Rectangle {
                color: KikimoraTheme.surfaceBase
                radius: KikimoraTheme.radiusSmall
                border.width: 1
                border.color: KikimoraTheme.borderSoft
            }
        }
    }
}

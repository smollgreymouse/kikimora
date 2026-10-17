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
                       + " endpoint=" + r.endpointState
                       + " published=" + (r.leshyPublished ? "yes" : "no")
                       + " parking=" + (r.parkingActive ? "active" : "inactive") + "/" + r.parkedRoutes
                       + " epoch=" + r.validatedUnderlayEpoch
                       + (r.recoveryAction ? (" recovery=" + r.recoveryAction) : "")
                       + (r.nextRetryAt ? (" next_retry=" + r.nextRetryAt) : "")
                       + (r.lastError ? (" error=" + r.lastError) : ""))
        }
        return lines.join("\n")
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

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "../Theme"

// Full role field surface (plan U5): driver/protocol, endpoint, interface,
// underlay, Leshy publication, parking, recovery and validation truth, plus the
// core-reported per-role actions. Values come from the core snapshot only; the
// drawer never infers state.
ColumnLayout {
    id: root
    property int roleIndex: -1
    property var roleData: ({})
    property var actions: (roleData && roleData.availableActions) ? roleData.availableActions : []
    signal roleActionRequested(int roleIndex, string action)

    function val(key, fallback) {
        var v = root.roleData ? root.roleData[key] : undefined
        if (v === undefined || v === null || v === "") return fallback !== undefined ? fallback : "\u2014"
        return String(v)
    }
    function yesNo(key) { return root.roleData && root.roleData[key] ? "yes" : "no" }
    function actionLabel(action) {
        switch (action) {
        case "connect": return "Connect this role"
        case "disconnect": return "Disconnect this role"
        case "retry": return "Retry this role"
        case "validate": return "Validate"
        case "rediscover": return "Rediscover endpoints"
        }
        return action
    }

    Layout.fillWidth: true
    spacing: 14

    RowLayout {
        Layout.fillWidth: true
        spacing: 10
        Text {
            objectName: "drawerRoleTitle"
            Layout.fillWidth: true
            text: root.roleData.label || "VPN role"
            color: KikimoraTheme.textPrimary
            font.pixelSize: 24
            font.weight: Font.Bold
            Accessible.role: Accessible.Heading
            Accessible.name: text
        }
        KStatusChip {
            objectName: "drawerRoleStateChip"
            state: root.val("state", "")
            text: root.val("stateText", "")
        }
    }

    Text {
        Layout.fillWidth: true
        text: root.val("protocol") + " \u00b7 " + root.val("server")
        color: KikimoraTheme.textSecondary
        font.pixelSize: 13
        wrapMode: Text.WordWrap
    }

    KButton {
        objectName: "roleActionButton"
        Layout.fillWidth: true
        Layout.topMargin: 10
        visible: root.actions.length > 0
        primary: true
        text: root.actions.length > 0 ? root.actionLabel(root.actions[0]) : ""
        accessibleText: text
        onClicked: {
            root.roleActionRequested(root.roleIndex, root.actions[0])
        }
    }

    Repeater {
        model: root.actions.slice(1)
        delegate: KButton {
            objectName: "roleSecondaryActionButton"
            required property string modelData
            Layout.fillWidth: true
            text: root.actionLabel(modelData)
            accessibleText: text
            onClicked: root.roleActionRequested(root.roleIndex, modelData)
        }
    }
    KSectionHeader { text: "Connection" }
    KInfoRow { label: "Driver"; value: root.val("protocol") }
    KInfoRow { label: "Desired"; value: root.yesNo("desiredEnabled") }
    KInfoRow { label: "Operation"; value: root.val("operation") }
    KInfoRow {
        label: "Interface"
        value: root.val("interfaceName")
               + (root.roleData.interfaceIndex !== undefined && root.roleData.interfaceIndex >= 0
                  ? " (ifindex " + root.roleData.interfaceIndex + ", mtu " + root.roleData.interfaceMtu + ")" : "")
    }
    KInfoRow {
        label: "Session"
        value: (root.roleData.sessionConnected ? "connected" : "not connected")
               + (root.roleData.sessionEndpoint ? " \u00b7 " + root.roleData.sessionEndpoint : "")
    }
    KInfoRow {
        label: "Traffic"
        value: "rx " + Math.round(root.roleData.sessionRxBytes || 0) + " B \u00b7 tx "
               + Math.round(root.roleData.sessionTxBytes || 0) + " B"
    }

    KDivider { Layout.fillWidth: true }

    KSectionHeader { text: "Routing and Leshy" }
    KInfoRow { label: "Physical underlay"; value: root.val("underlay") }
    KInfoRow { label: "Route target ready"; value: root.yesNo("routeReady") }
    KInfoRow { label: "Validated underlay epoch"; value: root.val("validatedUnderlayEpoch") }
    KInfoRow { label: "Endpoint policy"; value: root.val("endpointState") }
    KInfoRow {
        label: "Configured endpoints"
        value: (root.roleData.configuredEndpoints && root.roleData.configuredEndpoints.length > 0)
               ? root.roleData.configuredEndpoints.join(", ") : "\u2014"
    }
    KInfoRow {
        label: "Live endpoints"
        value: (root.roleData.liveEndpoints && root.roleData.liveEndpoints.length > 0)
               ? root.roleData.liveEndpoints.join(", ") : "\u2014"
    }
    KInfoRow { label: "Leshy zone"; value: root.val("leshyZone") }
    KInfoRow {
        label: "Leshy publication"
        value: root.roleData.leshyPublished ? "Published" : "Withdrawn"
    }
    KInfoRow {
        label: "Parking"
        value: (root.roleData.parkingActive ? "active" : "inactive")
               + " \u00b7 " + (root.roleData.parkedRoutes !== undefined ? root.roleData.parkedRoutes : 0) + " routes"
    }

    KDivider { Layout.fillWidth: true }

    KSectionHeader { text: "Recovery and validation" }
    KInfoRow { label: "Recovery step"; value: root.val("recoveryAction") }
    KInfoRow { label: "Recovery reason"; value: root.val("recoveryReason") }
    KInfoRow { label: "Recovery attempt"; value: root.val("recoveryAttempt") }
    KInfoRow {
        objectName: "drawerNextRetryRow"
        label: "Next retry"
        visible: root.roleData.nextRetryAt !== undefined && String(root.roleData.nextRetryAt).length > 0
        value: root.val("nextRetryAt")
    }
    KInfoRow {
        label: "Validation"
        value: (root.val("validationState", "").length > 0 ? root.val("validationState", "") : "\u2014")
               + (root.val("validationReason", "").length > 0 ? " \u00b7 " + root.val("validationReason", "") : "")
    }
    KInfoRow { label: "Last recovery"; value: root.val("lastRecovery") }
    KInfoRow { label: "Last error"; value: root.val("lastError") }

}

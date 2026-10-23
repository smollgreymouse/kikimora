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
    readonly property bool routeDirty: routeListEditor.text !== (Core.routingLists[String(root.roleData.label || "")] || "")
    function routePlural(n, one, few, many) {
        const n10 = n % 10
        const n100 = n % 100
        if (n10 === 1 && n100 !== 11) return one
        if (n10 >= 2 && n10 <= 4 && (n100 < 12 || n100 > 14)) return few
        return many
    }
    function routeLineIsValid(value) {
        const v = value.toLowerCase()
        if (/^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$/.test(v)) return true
        if (/^[0-9]{1,3}(\.[0-9]{1,3}){3}(\/[0-9]{1,2})?$/.test(v)) return true
        if (v.indexOf(":") >= 0 && /^[0-9a-f:]+(\/[0-9]{1,3})?$/.test(v)) return true
        return false
    }
    function routeCount(wantEntries) {
        const lines = routeListEditor.text.split("\n")
        let n = 0
        for (let i = 0; i < lines.length; ++i) {
            const line = lines[i].trim()
            if (line.length === 0) continue
            const isComment = line.charAt(0) === "#"
            if (isComment !== wantEntries) n += 1
        }
        return n
    }
    function routeInvalid() {
        const lines = routeListEditor.text.split("\n")
        let n = 0
        for (let i = 0; i < lines.length; ++i) {
            const line = lines[i].trim()
            if (line.length === 0 || line.charAt(0) === "#") continue
            if (!root.routeLineIsValid(line)) n += 1
        }
        return n
    }

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
    // The list is the role itself: there is nothing to choose, the zone row only shows it.
    KInfoRow { label: "Список Лешего"; value: root.val("leshyZone") }

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


    KDivider { Layout.fillWidth: true }

    KSectionHeader { text: "\u041c\u0430\u0440\u0448\u0440\u0443\u0442\u044b \u0440\u043e\u043b\u0438" }
    Text {
        Layout.fillWidth: true
        text: "\u0414\u043e\u043c\u0435\u043d\u044b \u0438 \u0430\u0434\u0440\u0435\u0441\u0430, \u043a\u043e\u0442\u043e\u0440\u044b\u0435 \u041b\u0435\u0448\u0438\u0439 \u0432\u0435\u0437\u0451\u0442 \u0447\u0435\u0440\u0435\u0437 \u044d\u0442\u043e\u0442 \u0442\u0443\u043d\u043d\u0435\u043b\u044c."
        color: KikimoraTheme.textSecondary
        font.pixelSize: 12
        wrapMode: Text.WordWrap
    }
    Rectangle {
        Layout.fillWidth: true
        Layout.preferredHeight: 150
        radius: KikimoraTheme.radiusSmall
        color: KikimoraTheme.surface
        border.width: 1
        border.color: KikimoraTheme.surfaceHover

        ScrollView {
            anchors.fill: parent
            anchors.margins: 6
            clip: true
            ScrollBar.vertical.policy: ScrollBar.AsNeeded

            TextArea {
                id: routeListEditor
                objectName: "routeListEditor"
                wrapMode: TextArea.Wrap
                background: null
                color: KikimoraTheme.textPrimary
                placeholderTextColor: KikimoraTheme.textSecondary
                selectionColor: KikimoraTheme.accent
                text: Core.routingLists[String(root.roleData.label || "")] || ""
            }
        }
    }
    Text {
        objectName: "routeListStatus"
        Layout.fillWidth: true
        wrapMode: Text.WordWrap
        font.pixelSize: 12
        color: root.routeInvalid() > 0 ? KikimoraTheme.danger : KikimoraTheme.textSecondary
        text: {
            if (routeListEditor.text.trim().length === 0)
                return "\u0421\u043f\u0438\u0441\u043e\u043a \u043f\u0443\u0441\u0442 \u2014 \u041b\u0435\u0448\u0438\u0439 \u043d\u0435 \u0431\u0443\u0434\u0435\u0442 \u0432\u0435\u0437\u0442\u0438 \u0442\u0440\u0430\u0444\u0438\u043a \u044d\u0442\u043e\u0439 \u0440\u043e\u043b\u0438."
            const entries = root.routeCount(true)
            const comments = root.routeCount(false)
            const invalid = root.routeInvalid()
            let out = entries + " " + root.routePlural(entries, "\u0437\u0430\u043f\u0438\u0441\u044c", "\u0437\u0430\u043f\u0438\u0441\u0438", "\u0437\u0430\u043f\u0438\u0441\u0435\u0439")
            if (comments > 0)
                out += " \u00b7 " + comments + " " + root.routePlural(comments, "\u043a\u043e\u043c\u043c\u0435\u043d\u0442\u0430\u0440\u0438\u0439", "\u043a\u043e\u043c\u043c\u0435\u043d\u0442\u0430\u0440\u0438\u044f", "\u043a\u043e\u043c\u043c\u0435\u043d\u0442\u0430\u0440\u0438\u0435\u0432")
            if (invalid > 0)
                out += " \u00b7 " + invalid + " " + root.routePlural(invalid, "\u0441\u0442\u0440\u043e\u043a\u0430 \u043d\u0435 \u0440\u0430\u0441\u043f\u043e\u0437\u043d\u0430\u043d\u0430", "\u0441\u0442\u0440\u043e\u043a\u0438 \u043d\u0435 \u0440\u0430\u0441\u043f\u043e\u0437\u043d\u0430\u043d\u044b", "\u0441\u0442\u0440\u043e\u043a \u043d\u0435 \u0440\u0430\u0441\u043f\u043e\u0437\u043d\u0430\u043d\u044b")
            return out
        }
    }
    RowLayout {
        Layout.fillWidth: true
        spacing: 10

        KButton {
            objectName: "routeListSaveButton"
            Layout.fillWidth: true
            Layout.preferredHeight: 40
            text: "\u0421\u043e\u0445\u0440\u0430\u043d\u0438\u0442\u044c"
            primary: true
            enabled: root.routeDirty && root.routeInvalid() === 0
            onClicked: Core.saveRoutingList(String(root.roleData.label || ""), routeListEditor.text)
        }

        Text {
            objectName: "routeListHint"
            Layout.fillWidth: true
            Layout.preferredWidth: 0
            visible: text.length > 0
            text: Core.lastCommandMessage
            color: text.indexOf("saved") >= 0 ? KikimoraTheme.success : KikimoraTheme.danger
            font.pixelSize: 12
            wrapMode: Text.WordWrap
        }
    }

    // The editor follows the panel: on a role switch it re-reads the stored list, and a late
    // snapshot only fills it while the operator has not typed anything.
    onRoleDataChanged: routeListEditor.text = Core.routingLists[String(root.roleData.label || "")] || ""

    Connections {
        target: Core
        function onRoutingListsChanged() {
            const stored = Core.routingLists[String(root.roleData.label || "")] || ""
            if (!root.routeDirty && routeListEditor.text !== stored)
                routeListEditor.text = stored
        }
    }
}

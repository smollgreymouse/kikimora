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
    property int sectionIndex: 0  // 0 = settings, 1 = routes
    property bool diagnosticsExpanded: false
    property int roleIndex: -1
    property var roleData: ({})
    property var actions: (roleData && roleData.availableActions) ? roleData.availableActions : []
    signal roleActionRequested(int roleIndex, string action)

    // Membership draft for this role. Profiles are an explicit set, so the drawer edits a
    // list: one role may serve several profiles. "default" always contains every role and
    // is therefore not offered as a choice.
    property var draftProfiles: []

    function editableProfiles() {
        const revision = Core.revision
        const names = []
        for (let i = 0; i < Core.profiles.length; ++i) {
            const name = String(Core.profiles[i])
            if (name !== "default") names.push(name)
        }
        return names
    }

    function savedProfiles() {
        const revision = Core.revision
        const names = Core.profilesForRole(String(root.roleData.label || "")) || []
        return names.filter(function(name) { return name !== "default" })
    }

    function profileSelected(name) {
        return root.draftProfiles.indexOf(name) !== -1
    }

    function setProfileSelected(name, selected) {
        const next = root.draftProfiles.filter(function(entry) { return entry !== name })
        if (selected) next.push(name)
        root.draftProfiles = next
    }

    function resetProfileDraft() {
        root.draftProfiles = root.savedProfiles()
    }

    readonly property bool profilesDirty: {
        const draft = root.draftProfiles.slice().sort().join(",")
        const saved = root.savedProfiles().slice().sort().join(",")
        return draft !== saved
    }

    Component.onCompleted: resetProfileDraft()

    function discardRoutes() {
        routeListEditor.text = Core.routingLists[String(root.roleData.label || "")] || ""
    }

    function val(key, fallback) {
        var v = root.roleData ? root.roleData[key] : undefined
        if (v === undefined || v === null || v === "") return fallback !== undefined ? fallback : "\u2014"
        return String(v)
    }
    function yesNo(key) { return root.roleData && root.roleData[key] ? "yes" : "no" }
    function normalizedRoutes(raw) {
        const out = []
        const lines = String(raw || "").split("\n")
        for (let i = 0; i < lines.length; ++i) {
            const line = lines[i].trim()
            if (line.length > 0 && line.charAt(0) !== "#")
                out.push(line.toLowerCase())
        }
        return out.join("\n")
    }
    readonly property bool routeDirty: normalizedRoutes(routeListEditor.text) !==
                                       normalizedRoutes(Core.routingLists[String(root.roleData.label || "")] || "")
    function routePlural(n, one, few, many) {
        const n10 = n % 10
        const n100 = n % 100
        if (n10 === 1 && n100 !== 11) return one
        if (n10 >= 2 && n10 <= 4 && (n100 < 12 || n100 > 14)) return few
        return many
    }
    function routeLineIsValid(value) {
        const v = value.toLowerCase()
        // Numeric dotted addresses must pass IP range validation; the domain
        // grammar alone would mistakenly accept 999.999.999.999.
        if (/^[0-9.]+(\/[0-9]+)?$/.test(v) &&
            !/^[0-9]{1,3}(\.[0-9]{1,3}){3}(\/[0-9]{1,2})?$/.test(v))
            return false
        if (/^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$/.test(v)
            && !/^[0-9.]+$/.test(v)) return true
        if (/^[0-9]{1,3}(\.[0-9]{1,3}){3}(\/[0-9]{1,2})?$/.test(v)) {
            const parts = v.split("/")
            if (parts.length > 1 && Number(parts[1]) > 32) return false
            return parts[0].split(".").every(p => Number(p) <= 255)
        }
        if (v.indexOf(":") >= 0 && /^[0-9a-f:]+(\/[0-9]{1,3})?$/.test(v)) {
            const parts = v.split("/")
            return parts.length === 1 || Number(parts[1]) <= 128
        }
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
    RowLayout {
        Layout.fillWidth: true
        spacing: 8
        KButton {
            objectName: "roleSettingsTab"
            Layout.fillWidth: true
            text: "Настройки"
            primary: root.sectionIndex === 0
            onClicked: root.sectionIndex = 0
        }
        KButton {
            objectName: "roleRoutesTab"
            Layout.fillWidth: true
            text: root.routeDirty ? "Маршруты •" : "Маршруты"
            primary: root.sectionIndex === 1
            onClicked: root.sectionIndex = 1
        }
    }

    ColumnLayout {
        id: roleSettingsSection
        Layout.fillWidth: true
        visible: root.sectionIndex === 0
        spacing: 10
        KSectionHeader { text: "Настройки роли" }
        KInfoRow { label: "Протокол"; value: root.val("protocol") }
        KInfoRow { label: "Сервер"; value: root.val("server") }
        KInfoRow { label: "Интерфейс"; value: root.val("interfaceName") }

        Text {
            Layout.fillWidth: true
            text: "Профили роли"
            color: KikimoraTheme.textSecondary
            font.pixelSize: 12
        }
        Column {
            id: roleProfileChoices
            Layout.fillWidth: true
            spacing: 2
            Repeater {
                model: root.editableProfiles()
                delegate: KCheckRow {
                    objectName: "roleProfileOption"
                    width: roleProfileChoices.width
                    label: String(modelData)
                    checked: root.profileSelected(String(modelData))
                    onToggled: function(checked) { root.setProfileSelected(String(modelData), checked) }
                }
            }
        }
        Text {
            Layout.fillWidth: true
            visible: root.editableProfiles().length === 0
            text: "Других профилей нет: роль видна только в default."
            color: KikimoraTheme.textSecondary
            font.pixelSize: 11
            wrapMode: Text.WordWrap
        }
        KButton {
            objectName: "roleProfileSaveButton"
            Layout.fillWidth: true
            text: "Сохранить профили"
            enabled: root.profilesDirty
            onClicked: Core.setTunnelProfiles(String(root.roleData.label || ""),
                                              root.draftProfiles.length > 0
                                                  ? root.draftProfiles : ["default"])
        }

        Text {
            Layout.fillWidth: true
            text: "Роль может входить в несколько профилей; default всегда содержит все роли. Адрес и параметры протокола пока не редактируются API ядра."
            wrapMode: Text.WordWrap
            color: KikimoraTheme.textSecondary
            font.pixelSize: 11
        }

        KButton {
            objectName: "roleRemoveButton"
            Layout.fillWidth: true
            text: "Удалить роль\u2026"
            onClicked: roleRemoveDialog.open()
        }
        KDivider { Layout.fillWidth: true }

    }

    Dialog {
        id: roleRemoveDialog
        objectName: "roleRemoveDialog"
        parent: Overlay.overlay
        anchors.centerIn: parent
        width: Math.min(parent ? parent.width - 32 : 350, 380)
        modal: true
        title: "Удалить роль"
        standardButtons: Dialog.Cancel
        contentItem: ColumnLayout {
            spacing: 8
            Label {
                Layout.fillWidth: true
                wrapMode: Text.WordWrap
                color: KikimoraTheme.textPrimary
                text: "Конфиг туннеля «" + (root.roleData.label || "") + "» будет удалён с диска. Роль исчезнет из списка после рестарта ядра — перезапустите его, чтобы изменение применилось."
            }
        }
        footer: DialogButtonBox {
            KButton {
                text: "Отмена"
                onClicked: roleRemoveDialog.reject()
            }
            KButton {
                objectName: "roleRemoveConfirmButton"
                text: "Удалить"
                onClicked: {
                    Core.removeTunnel(String(root.roleData.label || ""))
                    roleRemoveDialog.accept()
                }
            }
        }
    }

    ColumnLayout {
        id: roleRoutesSection
        Layout.fillWidth: true
        visible: root.sectionIndex === 1
        spacing: 10
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
            color: root.routeInvalid() > 0 ? KikimoraTheme.error : KikimoraTheme.textSecondary
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
                color: text.indexOf("saved") >= 0 ? KikimoraTheme.success : KikimoraTheme.error
                font.pixelSize: 12
                wrapMode: Text.WordWrap
            }
        }

    }

    KDivider { Layout.fillWidth: true }

    KButton {
        objectName: "roleDiagnosticsToggle"
        visible: root.sectionIndex === 0
        Layout.fillWidth: true
        text: root.diagnosticsExpanded ? "Скрыть диагностику" : "Техническая информация"
        onClicked: root.diagnosticsExpanded = !root.diagnosticsExpanded
    }
    ColumnLayout {
        id: diagnosticsDetails
        visible: root.diagnosticsExpanded && root.sectionIndex === 0
        Layout.fillWidth: true
        spacing: 12
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
            label: "Handshake age"
            value: (root.roleData.lastHandshakeAgeMs !== undefined && root.roleData.lastHandshakeAgeMs >= 0)
                   ? Math.round(root.roleData.lastHandshakeAgeMs / 1000) + " с назад" : "\u2014"
        }
        KInfoRow {
            label: "Traffic"
            value: "rx " + Math.round(root.roleData.sessionRxBytes || 0) + " B \u00b7 tx "
                   + Math.round(root.roleData.sessionTxBytes || 0) + " B"
        }

        KDivider { Layout.fillWidth: true }

        KSectionHeader { text: "Routing and Leshy" }
        KInfoRow { label: "Physical underlay"; value: Core.underlaySummary }
        KInfoRow { label: "Route target ready"; value: root.yesNo("routeReady") }
        KInfoRow { label: "Validated underlay epoch"; value: root.val("validatedUnderlayEpoch") }
        KInfoRow {
            label: "Текущая эпоха underlay"
            value: (Core.underlay && Core.underlay.epoch !== undefined)
                   ? String(Core.underlay.epoch) : "—"
        }
        KInfoRow {
            objectName: "drawerEpochLagRow"
            label: "Перевалидация"
            visible: {
                const validated = Number(root.roleData.validatedUnderlayEpoch || 0)
                const live = Number(Core.underlay && Core.underlay.epoch !== undefined ? Core.underlay.epoch : 0)
                return validated > 0 && live > 0 && validated < live
            }
            value: "нужна перевалидация"
            valueColor: KikimoraTheme.warning
        }
        KInfoRow { label: "Endpoint policy"; value: root.val("endpointState") }
        KInfoRow {
            label: "Resolved endpoints"
            value: (root.roleData.resolvedEndpoints && root.roleData.resolvedEndpoints.length > 0)
                   ? root.roleData.resolvedEndpoints.join(", ") : "\u2014"
        }
        KInfoRow {
            label: "Endpoint epoch"
            value: (root.roleData.appliedUnderlayEpoch !== undefined && root.roleData.appliedUnderlayEpoch > 0)
                   ? String(root.roleData.appliedUnderlayEpoch) : "\u2014"
        }
        KInfoRow {
            label: "Endpoint error"
            value: (root.roleData.endpointLastError && root.roleData.endpointLastError.length > 0)
                   ? root.roleData.endpointLastError : "\u2014"
        }
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
                   + ((root.roleData.parkedPrefixes && root.roleData.parkedPrefixes.length > 0)
                      ? " \u00b7 " + root.roleData.parkedPrefixes.join(", ") : "")
        }
        KInfoRow {
            label: "PID · Generation"
            value: (root.roleData.pid !== undefined && root.roleData.pid > 0 ? String(root.roleData.pid) : "\u2014")
                   + " \u00b7 "
                   + (root.roleData.generation !== undefined && root.roleData.generation > 0 ? String(root.roleData.generation) : "\u2014")
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

    }

    // The editor follows the panel: on a role switch it re-reads the stored list and the
    // saved profile membership, and a late snapshot only fills them while the operator has
    // not typed anything.
    onRoleDataChanged: {
        discardRoutes()
        resetProfileDraft()
    }

    Connections {
        target: Core
        function onSnapshotChanged() {
            const stored = Core.routingLists[String(root.roleData.label || "")] || ""
            if (!root.routeDirty && routeListEditor.text !== stored)
                routeListEditor.text = stored
        }
    }
}

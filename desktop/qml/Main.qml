import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "Controls"
import "Drawers"
import "Pages"
import "Theme"

ApplicationWindow {
    id: window
    objectName: "mainWindow"
    visible: true
    width: 360
    height: 720
    minimumWidth: 340
    minimumHeight: 620
    title: "Kikimora"
    color: KikimoraTheme.background

    onClosing: function(close) {
        if (Desktop.trayAvailable && !Desktop.quitting) {
            close.accepted = false
            window.hide()
        } else if (!Desktop.quitting) {
            Desktop.quit()
        }
    }

    property int selectedTab: 0

    ColumnLayout {
        anchors.fill: parent
        spacing: 0

        // Quiet utility header: underlay on the left, aggregate state on the right.
        // The large connect control below remains the visual focal point.
        Item {
            Layout.fillWidth: true
            Layout.minimumHeight: 48
            Layout.preferredHeight: 48
            Layout.maximumHeight: 48

            RowLayout {
                anchors.fill: parent
                anchors.leftMargin: 18
                anchors.rightMargin: 18
                spacing: 10

                Text {
                    objectName: "headerUnderlay"
                    Layout.fillWidth: true
                    text: Core.underlaySummary
                    color: KikimoraTheme.textSecondary
                    font.pixelSize: 11
                    elide: Text.ElideRight
                    verticalAlignment: Text.AlignVCenter
                }

                KTag {
                    objectName: "simulatedTag"
                    visible: Core.simulated
                    text: "SIMULATED"
                    tagColor: KikimoraTheme.warning
                }

                Text {
                    id: coreStatus
                    objectName: "coreStatus"

                    property int failedRoles: {
                        var tick = Core.revision
                        var count = 0
                        for (var i = 0; i < Core.roles.rowCount(); ++i) {
                            var state = Core.roles.get(i).state
                            if (state === "Failed" || state === "Error")
                                count++
                        }
                        return count
                    }

                    text: Core.aggregateStateText.length > 0
                          ? Core.aggregateStateText
                            + (failedRoles > 0
                               ? " · " + failedRoles + " из " + Core.roles.rowCount() + " не поднялась"
                               : "")
                          : Core.coreState

                    color: Core.aggregateState === "Degraded" ? KikimoraTheme.warning
                           : Core.aggregateState === "Failed" ? KikimoraTheme.error
                           : Core.aggregateState === "Ready" ? KikimoraTheme.accentPrimary
                           : KikimoraTheme.textSecondary
                    font.pixelSize: 11
                    font.weight: Font.Medium
                    horizontalAlignment: Text.AlignRight
                }
            }
        }

        StackLayout {
            objectName: "contentStack"
            Layout.fillWidth: true
            Layout.fillHeight: true
            currentIndex: window.selectedTab

            Item {
                ColumnLayout {
                    anchors.fill: parent
                    spacing: 0

                    Item {
                        Layout.fillWidth: true
                        Layout.minimumHeight: 250
                        Layout.preferredHeight: 250
                        Layout.maximumHeight: 250

                        KConnectControl {
                            objectName: "connectArea"
                            anchors.fill: parent
                            stateText: Core.aggregateStateText
                            actionText: Core.aggregateActionText
                            onActivated: Core.toggleAll()
                        }

                        // FakeCore-only affordance used by the deterministic UI suite.
                        // It stays out of the production hierarchy instead of becoming
                        // a permanent card between the connect ring and the role sheet.
                        KButton {
                            id: nextDemoButton
                            objectName: "nextDemoButton"
                            visible: Core.simulated
                            anchors.right: parent.right
                            anchors.top: parent.top
                            anchors.rightMargin: 14
                            anchors.topMargin: 4
                            implicitWidth: 68
                            implicitHeight: 30
                            text: "Demo"
                            onClicked: Core.nextDemoScenario()
                        }
                    }

                    // The active profile becomes the Amnezia-like lower sheet: one
                    // large context title, compact role rows and the add action.
                    Rectangle {
                        id: homeRoleSheet
                        Layout.fillWidth: true
                        Layout.fillHeight: true
                        Layout.minimumHeight: 250
                        radius: KikimoraTheme.radiusLarge
                        color: KikimoraTheme.surface

                        ColumnLayout {
                            anchors.fill: parent
                            anchors.leftMargin: 14
                            anchors.rightMargin: 14
                            anchors.topMargin: 10
                            anchors.bottomMargin: 12
                            spacing: 9

                            Rectangle {
                                Layout.alignment: Qt.AlignHCenter
                                Layout.preferredWidth: 38
                                Layout.preferredHeight: 4
                                radius: 2
                                color: KikimoraTheme.border
                            }

                            RowLayout {
                                Layout.fillWidth: true
                                spacing: 10

                                ColumnLayout {
                                    Layout.fillWidth: true
                                    spacing: 1

                                    Text {
                                        Layout.fillWidth: true
                                        text: Core.activeProfile.length > 0 ? Core.activeProfile : "default"
                                        color: KikimoraTheme.textPrimary
                                        font.pixelSize: 23
                                        font.weight: Font.DemiBold
                                        elide: Text.ElideRight
                                    }

                                    Text {
                                        Layout.fillWidth: true
                                        text: "VPN-роли · " + Core.roles.rowCount()
                                        color: KikimoraTheme.textSecondary
                                        font.pixelSize: 11
                                    }
                                }

                                Text {
                                    text: "⌄"
                                    color: KikimoraTheme.textMuted
                                    font.pixelSize: 22
                                }
                            }

                            ListView {
                                id: roleList
                                objectName: "roleList"
                                Layout.fillWidth: true
                                Layout.fillHeight: true
                                spacing: 8
                                clip: true
                                model: Core.roles

                                delegate: Item {
                                    id: roleDelegate
                                    objectName: "roleDelegate"
                                    required property int index
                                    required property string label
                                    required property string protocol
                                    required property string server
                                    required property string state
                                    required property string stateText

                                    width: ListView.view.width
                                    height: roleRow.implicitHeight

                                    KRoleSummaryRow {
                                        id: roleRow
                                        anchors.fill: parent
                                        label: roleDelegate.label
                                        protocol: roleDelegate.protocol
                                        server: roleDelegate.server
                                        state: roleDelegate.state
                                        stateText: roleDelegate.stateText
                                        onOpenRequested: {
                                            roleDrawer.roleIndex = roleDelegate.index
                                            roleDrawer.roleData = Core.roles.get(roleDelegate.index)
                                            roleDrawer.open()
                                        }
                                    }
                                }
                            }

                            Rectangle {
                                objectName: "openTunnelWizardButton"
                                Layout.fillWidth: true
                                Layout.minimumHeight: 52
                                Layout.preferredHeight: 52
                                Layout.maximumHeight: 52
                                radius: KikimoraTheme.radiusSmall
                                color: addTunnelMouse.containsMouse
                                       ? KikimoraTheme.surfaceHover
                                       : KikimoraTheme.surfaceRaised
                                Accessible.role: Accessible.Button
                                Accessible.name: "Добавить туннель"

                                Text {
                                    anchors.centerIn: parent
                                    text: "+"
                                    color: KikimoraTheme.textSecondary
                                    font.pixelSize: 28
                                    font.weight: Font.Light
                                }

                                MouseArea {
                                    id: addTunnelMouse
                                    anchors.fill: parent
                                    hoverEnabled: true
                                    cursorShape: Qt.PointingHandCursor
                                    onClicked: {
                                        profileDrawer.profileName = Core.activeProfile.length > 0
                                                ? Core.activeProfile : "default"
                                        profileDrawer.roles = Core.roles
                                        profileDrawer.open()
                                    }
                                }
                            }
                        }
                    }
                }
            }

            Item {
                id: profilesPage
                property string titleText: "Profiles"

                ScrollView {
                    id: profilesScroll
                    anchors.fill: parent
                    anchors.margins: 14
                    clip: true
                    contentWidth: availableWidth
                    ScrollBar.vertical.policy: ScrollBar.AsNeeded
                    ScrollBar.horizontal.policy: ScrollBar.AlwaysOff

                    ColumnLayout {
                        width: profilesScroll.availableWidth
                        spacing: KikimoraTheme.spacing

                        Text {
                            Layout.fillWidth: true
                            text: "Профиль — это набор ролей. Нажмите на профиль: он включится, а роли и добавление туннеля выедут снизу."
                            color: KikimoraTheme.textSecondary
                            font.pixelSize: 12
                            wrapMode: Text.WordWrap
                        }

                        // Kept for the live tests and for anything that switches programmatically.
                        KComboBox {
                            objectName: "profileSelector"
                            comboObjectName: "profileCombo"
                            Layout.fillWidth: true
                            visible: false
                            model: Core.profiles
                            textRole: "modelData"
                            currentIndex: Core.profiles.indexOf(Core.activeProfile)
                            onActivated: function(index) {
                                Core.setActiveProfile(Core.profiles[index])
                            }
                        }

                        KBarsPanel {
                            objectName: "profilesBars"
                            model: Core.profiles
                            activeValue: Core.activeProfile
                            plusObjectName: "openProfileEditorButton"
                            plusHint: "Новый профиль"

                            onRowClicked: function(index, value) {
                                if (Core.activeProfile !== value)
                                    Core.setActiveProfile(value)
                                profileDrawer.profileName = value
                                profileDrawer.open()
                            }
                            onPlusClicked: profileEditor.visible = !profileEditor.visible
                        }

                        ColumnLayout {
                            id: profileEditor
                            objectName: "profileEditor"
                            Layout.fillWidth: true
                            visible: false
                            spacing: KikimoraTheme.spacing

                            KTextField {
                                id: newProfileField
                                objectName: "newProfileField"
                                Layout.fillWidth: true
                                label: "Имя профиля"
                                readOnly: false
                            }

                            KButton {
                                objectName: "createProfileButton"
                                Layout.fillWidth: true
                                Layout.preferredHeight: 40
                                text: "Создать профиль"
                                onClicked: Core.createProfile(newProfileField.value)
                            }
                        }

                        Text {
                            objectName: "restartHint"
                            Layout.fillWidth: true
                            visible: Core.lastCommandMessage.length > 0
                            text: Core.lastCommandMessage
                            color: KikimoraTheme.textSecondary
                            font.pixelSize: 12
                            wrapMode: Text.WordWrap
                        }
                    }
                }
            }

            Item {
                id: routingPage
                // The lists are read from the core as soon as the page exists: the operator never
                // presses a "load" button for data the core already owns.
                Component.onCompleted: Core.refreshRoutingLists()
                objectName: "routingPage"
                property string titleText: "Маршруты"

                // Live feedback for the editor: how many entries the list holds, whether the operator
                // changed anything, and which lines the core would reject before the request is sent.
                readonly property int entryCount: routingPage.countLines(true)
                readonly property int commentCount: routingPage.countLines(false)
                readonly property int invalidLines: routingPage.countInvalid()
                readonly property bool dirty:
                    routeListEditor.text !== (Core.routingLists[routeListCombo.model[routeListCombo.currentIndex]] || "")

                function plural(n, one, few, many) {
                    const n10 = n % 10
                    const n100 = n % 100
                    if (n10 === 1 && n100 !== 11) return one
                    if (n10 >= 2 && n10 <= 4 && (n100 < 12 || n100 > 14)) return few
                    return many
                }

                // Mirrors the validator in the core closely enough to warn before saving.
                function lineIsValid(value) {
                    const v = value.toLowerCase()
                    if (/^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$/.test(v)) return true
                    if (/^[0-9]{1,3}(\.[0-9]{1,3}){3}(\/[0-9]{1,2})?$/.test(v)) return true
                    if (v.indexOf(":") >= 0 && /^[0-9a-f:]+(\/[0-9]{1,3})?$/.test(v)) return true
                    return false
                }

                function countInvalid() {
                    const lines = routeListEditor.text.split("\n")
                    let invalid = 0
                    for (let i = 0; i < lines.length; ++i) {
                        const line = lines[i].trim()
                        if (line.length === 0 || line.charAt(0) === "#") continue
                        if (!routingPage.lineIsValid(line)) invalid += 1
                    }
                    return invalid
                }

                function countLines(entries) {
                    const lines = routeListEditor.text.split("\n")
                    let n = 0
                    for (let i = 0; i < lines.length; ++i) {
                        const line = lines[i].trim()
                        if (line.length === 0) continue
                        const isComment = line.charAt(0) === "#"
                        if (isComment !== entries) n += 1
                    }
                    return n
                }
                Flickable {
                    anchors.fill: parent
                    contentHeight: routingColumn.implicitHeight + 28
                    clip: true
                    ColumnLayout {
                        id: routingColumn
                        x: 14
                        width: parent.width - 28

                        // Whether the router exists on this platform decides if any of this has an effect.
                        Text {
                            Layout.fillWidth: true
                            text: Core.leshySupported
                                  ? "\u041b\u0435\u0448\u0438\u0439 \u0432\u043a\u043b\u044e\u0447\u0451\u043d"
                                  : "\u041b\u0435\u0448\u0438\u0439 \u043d\u0435\u0434\u043e\u0441\u0442\u0443\u043f\u0435\u043d"
                            color: Core.leshySupported ? KikimoraTheme.textSecondary : KikimoraTheme.danger
                            font.pixelSize: 12
                            wrapMode: Text.WordWrap
                        }

                        KSectionHeader { text: "Списки ролей" }

                            Text {
                                Layout.fillWidth: true
                                text: "У каждой роли свой список: Леший везёт его трафик через туннель этой роли. "
                                  + "Одна строка — домен, *.домен, IP или CIDR; строки с # остаются комментариями."
                                color: KikimoraTheme.textSecondary
                                font.pixelSize: 12
                                wrapMode: Text.WordWrap
                            }

                            Text {
                                objectName: "routeListEmpty"
                                Layout.fillWidth: true
                                visible: Core.roles.rowCount() === 0
                                text: "Ролей пока нет: добавьте туннель на главной, и у него появится свой список."
                                color: KikimoraTheme.textSecondary
                                font.pixelSize: 13
                                wrapMode: Text.WordWrap
                            }

                            KComboBox {
                                id: routeListCombo
                                objectName: "routeListCombo"
                                Layout.fillWidth: true
                                visible: Core.roles.rowCount() > 0
                                label: "Роль"
                                model: {
                                    const names = []
                                    for (let i = 0; i < Core.roles.rowCount(); ++i) names.push(String(Core.roles.get(i).label))
                                    return names
                                }
                                onCurrentIndexChanged: routeListEditor.text = Core.routingLists[model[currentIndex]] || ""
                            }

                            Rectangle {
                                Layout.fillWidth: true
                                Layout.preferredHeight: 170
                                visible: Core.roles.rowCount() > 0
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
                                        text: Core.routingLists[routeListCombo.model[routeListCombo.currentIndex]] || ""
                                    }
                                }
                            }

                            Text {
                                objectName: "routeListStatus"
                                Layout.fillWidth: true
                                visible: Core.roles.rowCount() > 0
                                wrapMode: Text.WordWrap
                                font.pixelSize: 12
                                color: routingPage.invalidLines > 0 ? KikimoraTheme.danger : KikimoraTheme.textSecondary
                                text: {
                                    const empty = routeListEditor.length === 0
                                    if (empty)
                                        return "Список пуст — Леший не будет везти трафик этой роли."
                                    let result = routingPage.entryCount + " " + routingPage.plural(routingPage.entryCount, "запись", "записи", "записей")
                                    if (routingPage.commentCount > 0)
                                        result += " · " + routingPage.commentCount + " " + routingPage.plural(routingPage.commentCount, "комментарий", "комментария", "комментариев")
                                    if (routingPage.invalidLines > 0)
                                        result += " · " + routingPage.invalidLines + " " + routingPage.plural(routingPage.invalidLines, "строка не распознана", "строки не распознаны", "строк не распознаны")
                                    return result
                                }
                            }

                            RowLayout {
                                Layout.fillWidth: true
                                spacing: 10

                                KButton {
                                    objectName: "routeListSaveButton"
                                    Layout.fillWidth: true
                                    Layout.preferredHeight: 40
                                    text: "Сохранить"
                                    primary: true
                                    enabled: Core.roles.rowCount() > 0 && routingPage.dirty && routingPage.invalidLines === 0
                                    onClicked: Core.saveRoutingList(routeListCombo.model[routeListCombo.currentIndex], routeListEditor.text)
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

                            Connections {
                                target: Core
                                function onRoutingListsChanged() {
                                    routeListEditor.text = Core.routingLists[routeListCombo.model[routeListCombo.currentIndex]] || ""
                                }
                            }

                            KSectionHeader { text: "Всё остальное" }

                            Text {
                                Layout.fillWidth: true
                                text: "Трафик, который не попал ни в один список ролей. Напрямую или через туннель одной роли — это глобальное правило Лешего."
                                color: KikimoraTheme.textSecondary
                                font.pixelSize: 12
                                wrapMode: Text.WordWrap
                            }

                            RowLayout {
                                Layout.fillWidth: true
                                spacing: 8

                                KComboBox {
                                    id: bypassModeCombo
                                    objectName: "bypassModeCombo"
                                    Layout.fillWidth: true
                                    label: "Куда"
                                    model: {
                                        const modes = ["direct"]
                                        for (let i = 0; i < Core.roles.rowCount(); ++i) modes.push(String(Core.roles.get(i).label))
                                        return modes
                                    }
                                    currentIndex: Math.max(0, model.indexOf(Core.bypassMode))
                                }

                                KButton {
                                    objectName: "bypassModeApply"
                                    Layout.preferredHeight: 40
                                    text: "Применить"
                                    enabled: model[bypassModeCombo.currentIndex] !== Core.bypassMode
                                    onClicked: Core.setBypassMode(bypassModeCombo.model[bypassModeCombo.currentIndex])
                                }
                            }

                            Text {
                                objectName: "bypassModeCurrent"
                                Layout.fillWidth: true
                                text: Core.bypassMode === "direct"
                                      ? "Сейчас: всё остальное идёт напрямую."
                                      : "Сейчас: всё остальное идёт через туннель роли " + Core.bypassMode + "."
                                color: KikimoraTheme.textSecondary
                                font.pixelSize: 12
                                wrapMode: Text.WordWrap
                            }

                            Text {
                                Layout.fillWidth: true
                                text: "Зон в конфиге Лешего: " + Core.routingZones.length + ". Подробности по ролям — на странице «Диагностика»."
                                color: KikimoraTheme.textSecondary
                                font.pixelSize: 12
                                wrapMode: Text.WordWrap
                            }


                        Item { Layout.fillHeight: true }
                    }
                }
            }

            Item {
                id: settingsPage
                objectName: "settingsPage"
                property string titleText: "Settings"
                Flickable {
                    anchors.fill: parent
                    contentHeight: settingsColumn.implicitHeight + 28
                    clip: true
                    ColumnLayout {
                        id: settingsColumn
                        x: 14
                        width: parent.width - 28

                        KCard {
                            objectName: "simulatedMarker"
                            Layout.fillWidth: true
                            visible: Core.simulated
                            RowLayout {
                                Layout.fillWidth: true
                                spacing: 10
                                KTag {
                                    text: "SIMULATED"
                                    tagColor: KikimoraTheme.warning
                                }
                                Text {
                                    Layout.fillWidth: true
                                    text: Core.simulated
                                          ? "This build is not protecting this host. No VPN traffic is routed."
                                          : ""
                                    color: KikimoraTheme.textSecondary
                                    font.pixelSize: 12
                                    wrapMode: Text.WordWrap
                                }
                            }
                        }

                        KCard {
                            objectName: "settingsDiagnostics"
                            Layout.fillWidth: true
                            KSectionHeader { text: "Diagnostics" }
                            KInfoRow { label: "Backend"; value: Core.backendKind }
                            KInfoRow { label: "Platform"; value: Core.platformName }
                            KInfoRow { label: "Core state"; value: Core.coreState }
                            KInfoRow { label: "Revision"; value: String(Core.revision) }
                            KInfoRow { label: "Leshy"; value: Core.leshySupported ? "supported" : "unsupported" }
                            KInfoRow { label: "Profile"; value: Core.activeProfile }
                            KInfoRow { label: "Profiles"; value: Core.profiles.join(", ") }
                            KInfoRow { label: "Aggregate"; value: Core.aggregateStateText }
                            KInfoRow { label: "Underlay"; value: Core.underlaySummary }
                            KInfoRow {
                                label: "Last command error"
                                value: Core.lastCommandError.length > 0 ? Core.lastCommandError : "—"
                            }
                        }

                        KCard {
                            Layout.fillWidth: true
                            KSectionHeader { text: "Appearance" }
                            KSwitch {
                                objectName: "darkThemeSwitch"
                                Layout.fillWidth: true
                                label: "Тёмная тема"
                                checked: ThemeManager.dark
                                onToggled: function(checked) { ThemeManager.setDark(checked) }
                            }

                            KSwitch {
                                objectName: "reduceMotionSwitch"
                                Layout.fillWidth: true
                                label: "Reduce motion"
                                checked: !KikimoraTheme.motionEnabled
                                onToggled: function(checked) { KikimoraTheme.motionEnabled = !checked }
                            }
                            KInfoRow {
                                label: "HiDPI"
                                value: "handled by Qt (device pixel ratio " + Screen.devicePixelRatio.toFixed(2) + ")"
                            }
                        }

                        KCard {
                            Layout.fillWidth: true
                            KSectionHeader { text: "Desktop" }

                    KSectionHeader { text: "Запуск с системой" }

                    Text {
                        Layout.fillWidth: true
                        text: "Окно закрывается в трей; автозапуск стартует приложение сразу в трее."
                        color: KikimoraTheme.textSecondary
                        font.pixelSize: 12
                        wrapMode: Text.WordWrap
                    }

                    KButton {
                        objectName: "autostartToggle"
                        text: Desktop.autostartEnabled ? "Автозапуск: включён" : "Автозапуск: выключен"
                        onClicked: Desktop.setAutostartEnabled(!Desktop.autostartEnabled)
                    }

                    Text {
                        objectName: "autostartTarget"
                        Layout.fillWidth: true
                        text: Desktop.autostartTarget
                        visible: text.length > 0
                        color: KikimoraTheme.textSecondary
                        font.pixelSize: 11
                        wrapMode: Text.WrapAnywhere
                    }
                            KInfoRow {
                                label: "Tray icon"
                                value: Desktop.trayAvailable ? "available" : "unavailable"
                            }
                            RowLayout {
                                Layout.fillWidth: true
                                spacing: 10
                                KButton {
                                    objectName: "settingsShowButton"
                                    Layout.fillWidth: true
                                    text: "Show window"
                                    onClicked: Desktop.showWindow()
                                }
                                KButton {
                                    objectName: "settingsHideButton"
                                    Layout.fillWidth: true
                                    text: "Hide window"
                                    onClicked: Desktop.hideWindow()
                                }
                            }
                        }

                        Item { Layout.fillHeight: true }
                    }
                }
            }
        }


            DiagnosticsPage {
                id: diagnosticsPage
            }
        KBottomNav {
            objectName: "bottomNav"
            items: [
                { label: "Главная", iconName: "home" },
                { label: "Профили", iconName: "profiles" },
                { label: "Маршруты", iconName: "routing" },
                { label: "Настройки", iconName: "settings" },
                { label: "Диагностика", iconName: "diagnostics" }
            ]
            selectedTab: window.selectedTab
            onTabSelected: function(index) { window.selectedTab = index }
        }
    }

    ProfileDrawer {

        id: profileDrawer

        parent: Overlay.overlay

        roles: Core.roles

        profiles: Core.profiles

        onRoleMoveRequested: function(role, profile) { Core.setTunnelProfiles(role, [profile]) }

    }





    RoleDrawer {
        id: roleDrawer
        parent: Overlay.overlay
        onRoleActionRequested: function(index, action) { Core.roleActionByName(index, action) }
    }
}

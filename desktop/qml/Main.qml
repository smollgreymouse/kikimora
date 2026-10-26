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

                    color: Core.aggregateState === "Degraded"
                               || Core.aggregateState === "Recovering"
                               || Core.aggregateState === "PartiallyReady" ? KikimoraTheme.warning
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
                                KComboBox {
                                    objectName: "profileSelector"
                                    comboObjectName: "profileCombo"
                                    Layout.fillWidth: true
                                    Layout.preferredHeight: 44
                                    titleMode: true
                                    label: "Профиль"
                                    model: Core.profiles
                                    currentIndex: Math.max(0, Core.profiles.indexOf(Core.activeProfile))
                                    onActivated: function(index) {
                                        Core.setActiveProfile(String(Core.profiles[index]))
                                    }
                                }
                                KButton {
                                    objectName: "createProfileButton"
                                    text: "+"
                                    accessibleText: "Создать профиль"
                                    Layout.preferredWidth: 46
                                    Layout.preferredHeight: 42
                                    onClicked: createProfileDialog.open()
                                }
                            }
                            Text {
                                Layout.fillWidth: true
                                text: "VPN-роли"
                                color: KikimoraTheme.textSecondary
                                font.pixelSize: 12
                            }

                            ListView {
                                id: roleList
                                objectName: "roleList"
                                Layout.fillWidth: true
                                Layout.fillHeight: true
                                spacing: 8
                                clip: true

                                property var visibleSourceRows: []
                                // Roles only: the default-route row is appended to the model,
                                // so the empty-profile hint must not count it.
                                readonly property int roleCount: visibleSourceRows.length

                                function refreshVisibleRoles() {
                                    const filtered = []
                                    for (let i = 0; i < Core.roles.rowCount(); ++i) {
                                        const role = Core.roles.get(i)
                                        if (!role || !role.label) continue
                                        const membership = Core.profilesForRole(String(role.label)) || []
                                        // The core currently does not publish membership for
                                        // every role on startup. Unknown roles remain visible.
                                        if (membership.length > 0 &&
                                            membership.indexOf(Core.activeProfile) === -1)
                                            continue
                                        filtered.push(i)
                                    }
                                    // Stable delegate identity across state updates: reassigning
                                    // the entire model on *every* snapshot destroys rows while
                                    // users/tests still interact with them.
                                    if (filtered.join(",") !== visibleSourceRows.join(","))
                                        visibleSourceRows = filtered
                                }

                                Component.onCompleted: refreshVisibleRoles()
                                Connections {
                                    target: Core
                                    function onSnapshotChanged() { roleList.refreshVisibleRoles() }
                                }

                                // The last row of every profile list is the default-route
                                // rule: a rule about the roles above, not another tunnel.
                                model: visibleSourceRows.concat([-1])

                                function visibleRoleLabels() {
                                    const tick = Core.revision
                                    const labels = []
                                    for (let i = 0; i < visibleSourceRows.length; ++i) {
                                        const role = Core.roles.get(visibleSourceRows[i])
                                        if (role && role.label)
                                            labels.push(String(role.label))
                                    }
                                    return labels
                                }

                                delegate: Item {
                                    id: roleDelegate
                                    required property var modelData
                                    readonly property int sourceIndex: Number(modelData)
                                    readonly property var snapshot: {
                                        const revision = Core.revision
                                        return sourceIndex >= 0 ? Core.roles.get(sourceIndex) : null
                                    }
                                    width: ListView.view.width
                                    height: sourceIndex < 0 ? 64 : 70

                                    // The last model entry is the default-route row: no role
                                    // row is created for it, so the list keeps exactly one
                                    // row per real role.
                                    Loader {
                                        anchors.fill: parent
                                        active: roleDelegate.sourceIndex >= 0
                                        sourceComponent: Component {
                                            KRoleSummaryRow {
                                                anchors.fill: parent
                                                label: String(roleDelegate.snapshot.label || "")
                                                protocol: String(roleDelegate.snapshot.protocol || "")
                                                server: String(roleDelegate.snapshot.server || "")
                                                state: String(roleDelegate.snapshot.state || "")
                                                stateText: String(roleDelegate.snapshot.stateText || "")
                                                onOpenRequested: {
                                                    roleDrawer.roleIndex = roleDelegate.sourceIndex
                                                    roleDrawer.roleData = Core.roles.get(roleDelegate.sourceIndex)
                                                    roleDrawer.open()
                                                }
                                            }
                                        }
                                    }

                                    // Closes the list: where traffic no role claims goes,
                                    // chosen from this profile's real roles only.
                                    KDefaultRouteRow {
                                        anchors.fill: parent
                                        visible: roleDelegate.sourceIndex < 0
                                        roleLabels: roleList.visibleRoleLabels()
                                        mode: Core.bypassMode
                                        onModeSelected: function(mode) { Core.setBypassMode(mode) }
                                    }
                                }
                            }

                            Text {
                                Layout.fillWidth: true
                                visible: roleList.roleCount === 0
                                text: "У этого профиля пока нет ролей. Добавьте туннель кнопкой ниже."
                                wrapMode: Text.WordWrap
                                color: KikimoraTheme.textSecondary
                                font.pixelSize: 12
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
                            id: settingsPage
                            objectName: "settingsPage"
                            property string titleText: "Settings"
                            property bool diagnosticsExpanded: false
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
                                        Layout.fillWidth: true
                                        KSectionHeader { text: "Оформление" }
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
                                            label: "Уменьшить анимации"
                                            checked: !KikimoraTheme.motionEnabled
                                            onToggled: function(checked) { KikimoraTheme.motionEnabled = !checked }
                                        }
                                    }

                                    KCard {
                                        Layout.fillWidth: true
                                        KSectionHeader { text: "Рабочий стол" }

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
                                            label: "Значок в трее"
                                            value: Desktop.trayAvailable ? "доступен" : "недоступен"
                                        }
                                        RowLayout {
                                            Layout.fillWidth: true
                                            spacing: 10
                                            KButton {
                                                objectName: "settingsShowButton"
                                                Layout.fillWidth: true
                                                text: "Показать окно"
                                                onClicked: Desktop.showWindow()
                                            }
                                            KButton {
                                                objectName: "settingsHideButton"
                                                Layout.fillWidth: true
                                                text: "Скрыть окно"
                                                onClicked: Desktop.hideWindow()
                                            }
                                        }
                                    }

                                    KButton {
                                        objectName: "settingsDiagnosticsToggle"
                                        Layout.fillWidth: true
                                        text: settingsPage.diagnosticsExpanded
                                              ? "Скрыть диагностику" : "Показать диагностику"
                                        onClicked: {
                                            settingsPage.diagnosticsExpanded = !settingsPage.diagnosticsExpanded
                                            if (settingsPage.diagnosticsExpanded) Core.refreshDiagnostics()
                                        }
                                    }

                                    KCard {
                                        objectName: "settingsDiagnostics"
                                        Layout.fillWidth: true
                                        visible: settingsPage.diagnosticsExpanded
                                        KSectionHeader { text: "Диагностика" }
                                        KInfoRow { label: "Backend"; value: Core.backendKind }
                                        KInfoRow { label: "Platform"; value: Core.platformName }
                                        KInfoRow { label: "Core state"; value: Core.coreState }
                                        KInfoRow {
                                            label: "HiDPI"
                                            value: "Qt · device pixel ratio " + Screen.devicePixelRatio.toFixed(2)
                                        }
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

                                        KSectionHeader { text: "Ядро" }
                                        KInfoRow {
                                            label: "Версия ядра"
                                            value: Core.diagnostics.core_version !== undefined ? String(Core.diagnostics.core_version) : "—"
                                        }
                                        KInfoRow {
                                            label: "Версия API"
                                            value: Core.diagnostics.protocol_version !== undefined ? String(Core.diagnostics.protocol_version) : "—"
                                        }
                                        KInfoRow {
                                            label: "Платформа ядра"
                                            value: Core.diagnostics.platform !== undefined ? String(Core.diagnostics.platform) : "—"
                                        }
                                        KInfoRow {
                                            label: "Сокет"
                                            value: Core.diagnostics.socket_path !== undefined ? String(Core.diagnostics.socket_path) : "—"
                                        }
                                        KInfoRow {
                                            label: "Статус desired-state"
                                            value: Core.diagnostics.desired_state_status !== undefined && Core.diagnostics.desired_state_status.length > 0
                                                   ? String(Core.diagnostics.desired_state_status) : "—"
                                        }
                                        KInfoRow {
                                            label: "Владельцы интерфейсов"
                                            value: {
                                                const owners = Core.diagnostics.managed_interfaces
                                                if (!owners || owners.length === 0) return "—"
                                                const keys = Object.keys(owners)
                                                return keys.map(k => k + ": " + owners[k]).join(", ")
                                            }
                                        }

                                        KSectionHeader { text: "Наблюдатели" }
                                        KInfoRow {
                                            label: "netlink"
                                            value: Core.observers.netlink_healthy === true
                                                   ? "healthy"
                                                   : "unhealthy · " + (Core.observers.netlink_last_error || "—")
                                            valueColor: Core.observers.netlink_healthy === true
                                                        ? KikimoraTheme.textPrimary : KikimoraTheme.warning
                                        }
                                        KInfoRow {
                                            label: "underlay-converger"
                                            value: Core.observers.underlay_converger_healthy === true
                                                   ? "healthy"
                                                   : "unhealthy · " + (Core.observers.converger_last_error || "—")
                                            valueColor: Core.observers.underlay_converger_healthy === true
                                                        ? KikimoraTheme.textPrimary : KikimoraTheme.warning
                                        }
                                        KInfoRow {
                                            label: "sleep"
                                            value: Core.observers.sleep_healthy === true
                                                   ? "healthy"
                                                   : "unhealthy · " + (Core.observers.sleep_last_error || "—")
                                            valueColor: Core.observers.sleep_healthy === true
                                                        ? KikimoraTheme.textPrimary : KikimoraTheme.warning
                                        }
                                        KInfoRow {
                                            label: "NetworkManager"
                                            value: Core.observers.networkmanager_healthy === true
                                                   ? "healthy"
                                                   : "unhealthy · " + (Core.observers.networkmanager_last_error || "—")
                                            valueColor: Core.observers.networkmanager_healthy === true
                                                        ? KikimoraTheme.textPrimary : KikimoraTheme.warning
                                        }

                                        KButton {
                                            objectName: "fullDiagnosticsButton"
                                            Layout.fillWidth: true
                                            text: "Полная диагностика"
                                            onClicked: fullDiagnosticsDrawer.open()
                                        }
                                    }

                                    Item { Layout.fillHeight: true }
                                }
                            }
                        }
        }

        KBottomNav {
            objectName: "bottomNav"
            items: [
                { label: "\u0413\u043b\u0430\u0432\u043d\u0430\u044f", iconName: "home" },
                { label: "\u041d\u0430\u0441\u0442\u0440\u043e\u0439\u043a\u0438", iconName: "settings" }
            ]
            selectedTab: window.selectedTab
            onTabSelected: function(index) { window.selectedTab = index }
        }
    }

    // Creating a profile is a small modal sheet: it validates the name with the
    // core's own rule, stays open while the core answers, and never claims success
    // for a profile the running core has not loaded yet.
    Popup {
        id: createProfileDialog
        objectName: "createProfileDialog"
        parent: Overlay.overlay
        x: parent ? (parent.width - width) / 2 : 0
        y: parent ? (parent.height - height) / 2 : 0
        width: Math.min(window.width - 40, 340)
        modal: true
        focus: true
        padding: 18
        closePolicy: busy ? Popup.NoAutoClose : Popup.CloseOnEscape

        property bool busy: false
        property string errorText: ""
        property string createdName: ""

        function validateName(name) {
            if (name.length === 0)
                return "Введите название профиля."
            if (!/^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/.test(name))
                return "Латиница, цифры, точка, дефис или подчёркивание; первым — буква или цифра."
            if (Core.profiles.indexOf(name) !== -1)
                return "Профиль с таким названием уже есть."
            return ""
        }

        function submit() {
            if (busy || createdName.length > 0)
                return
            const name = profileNameInput.text.trim()
            const problem = validateName(name)
            errorText = problem
            if (problem.length > 0) {
                profileNameInput.forceActiveFocus()
                return
            }
            // The answer arrives through profileCreationFinished; the dialog must
            // not close before the core has actually written the profile.
            busy = true
            Core.createProfile(name)
        }

        function finishCreated(name) {
            close()
            Core.setActiveProfile(name)
        }

        onOpened: {
            busy = false
            errorText = ""
            createdName = ""
            profileNameInput.text = ""
            profileNameInput.forceActiveFocus()
        }

        Connections {
            target: Core
            function onProfileCreationFinished(name, ok, message) {
                if (!createProfileDialog.opened) return
                createProfileDialog.busy = false
                if (!ok) {
                    createProfileDialog.errorText = message.length > 0
                            ? message : "Ядро отклонило название профиля."
                    return
                }
                if (Core.profiles.indexOf(name) !== -1) {
                    // The core already lists it: land on the new profile.
                    createProfileDialog.finishCreated(name)
                } else {
                    // Older cores only write the configuration; keep the name and
                    // close as soon as a snapshot reports it.
                    createProfileDialog.createdName = name
                }
            }

            // A core that applies profiles at runtime reports the new name through the
            // snapshot, so the sheet closes when the profile is really in the list.
            function onSnapshotChanged() {
                if (!createProfileDialog.opened) return
                if (createProfileDialog.createdName.length > 0
                        && Core.profiles.indexOf(createProfileDialog.createdName) !== -1)
                    createProfileDialog.finishCreated(createProfileDialog.createdName)
            }
        }

        background: Rectangle {
            radius: KikimoraTheme.radiusLarge
            color: KikimoraTheme.surface
            border.width: 1
            border.color: KikimoraTheme.borderSoft
        }

        contentItem: ColumnLayout {
            spacing: 12

            Text {
                Layout.fillWidth: true
                text: createProfileDialog.createdName.length > 0 ? "Профиль записан"
                                                                : "Новый профиль"
                color: KikimoraTheme.textPrimary
                font.pixelSize: 18
                font.weight: Font.DemiBold
            }

            Text {
                Layout.fillWidth: true
                visible: createProfileDialog.createdName.length === 0
                text: "Профиль объединяет роли и их маршрут по умолчанию."
                color: KikimoraTheme.textSecondary
                font.pixelSize: 11
                wrapMode: Text.WordWrap
            }

            TextField {
                id: profileNameInput
                objectName: "profileNameInput"
                Layout.fillWidth: true
                Layout.preferredHeight: 44
                visible: createProfileDialog.createdName.length === 0
                enabled: !createProfileDialog.busy
                placeholderText: "Название профиля"
                placeholderTextColor: KikimoraTheme.textMuted
                maximumLength: 64
                selectByMouse: true
                leftPadding: 12
                rightPadding: 12
                color: KikimoraTheme.textPrimary
                selectionColor: KikimoraTheme.accent
                font.pixelSize: 14
                onTextChanged: createProfileDialog.errorText = ""
                onAccepted: createProfileDialog.submit()
                background: Rectangle {
                    radius: KikimoraTheme.radiusSmall
                    color: KikimoraTheme.surfaceRaised
                    border.width: createProfileDialog.errorText.length > 0
                                  || profileNameInput.activeFocus ? 2 : 1
                    border.color: createProfileDialog.errorText.length > 0
                                  ? KikimoraTheme.error
                                  : profileNameInput.activeFocus ? KikimoraTheme.accent
                                                                 : KikimoraTheme.borderSoft
                }
            }

            Text {
                objectName: "createProfileStatus"
                Layout.fillWidth: true
                visible: text.length > 0
                wrapMode: Text.WordWrap
                color: createProfileDialog.createdName.length > 0
                       ? KikimoraTheme.textSecondary
                       : createProfileDialog.errorText.length > 0
                         ? KikimoraTheme.error : KikimoraTheme.textSecondary
                font.pixelSize: 11
                text: {
                    if (createProfileDialog.createdName.length > 0)
                        return "«" + createProfileDialog.createdName
                             + "» появится в списке профилей после перезапуска ядра."
                    if (createProfileDialog.errorText.length > 0)
                        return createProfileDialog.errorText
                    return "Латиница, цифры, точка, дефис или подчёркивание."
                }
            }

            RowLayout {
                Layout.fillWidth: true
                spacing: 8

                KButton {
                    objectName: "createProfileCancel"
                    Layout.fillWidth: true
                    text: createProfileDialog.createdName.length > 0 ? "Понятно" : "Отмена"
                    enabled: !createProfileDialog.busy
                    onClicked: createProfileDialog.close()
                }

                KButton {
                    objectName: "createProfileSubmit"
                    Layout.fillWidth: true
                    visible: createProfileDialog.createdName.length === 0
                    primary: true
                    text: createProfileDialog.busy ? "Создаём…" : "Создать"
                    enabled: !createProfileDialog.busy
                    onClicked: createProfileDialog.submit()
                }
            }
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

    KDrawer {
        id: fullDiagnosticsDrawer
        objectName: "fullDiagnosticsDrawer"
        parent: Overlay.overlay

        DiagnosticsPage {
            Layout.fillWidth: true
            Layout.fillHeight: true
        }
    }
}

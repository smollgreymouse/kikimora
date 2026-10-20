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
        anchors.margins: 14
        spacing: KikimoraTheme.spacing

        RowLayout {
            Layout.fillWidth: true
            Layout.minimumHeight: 44
            Layout.preferredHeight: 44
            Layout.maximumHeight: 44
            Image {
                Layout.preferredWidth: 38
                Layout.preferredHeight: 38
                source: "qrc:/resources/kikimora.png"
                sourceSize: Qt.size(38, 38)
                smooth: true
            }
            ColumnLayout {
                Layout.fillWidth: true
                spacing: 2
                Text {
                    text: "Kikimora"
                    color: KikimoraTheme.textPrimary
                    font.pixelSize: 18
                    font.weight: Font.Bold
                }
                Text {
                    text: "Маршрутизация доменов"
                    color: KikimoraTheme.textSecondary
                    font.pixelSize: 11
                }
            }
            KTag {
                objectName: "simulatedTag"
                visible: Core.simulated
                text: "SIMULATED"
                tagColor: KikimoraTheme.warning
            }
            Rectangle {
                radius: 999
                color: KikimoraTheme.surfaceHover
                implicitWidth: coreStatus.implicitWidth + 16
                implicitHeight: coreStatus.implicitHeight + 8
                    Text {
                        id: coreStatus
                        objectName: "coreStatus"
                    anchors.centerIn: parent
                    text: Core.coreState + " · r" + Core.revision
                    color: Core.coreState === "Ready" || Core.coreState === "Connected"
                               ? KikimoraTheme.success : KikimoraTheme.textSecondary
                    font.pixelSize: 11
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
                    spacing: KikimoraTheme.spacing

                    Text {
                        objectName: "underlaySummary"
                        Layout.minimumHeight: 18
                        Layout.preferredHeight: 18
                        Layout.alignment: Qt.AlignHCenter
                        text: Core.underlaySummary
                        color: KikimoraTheme.textSecondary
                        font.pixelSize: 12
                    }

                    KConnectControl {
                        objectName: "connectArea"
                        Layout.fillWidth: true
                        Layout.minimumHeight: 210
                        Layout.preferredHeight: 210
                        Layout.maximumHeight: 210
                        stateText: Core.aggregateStateText
                        actionText: Core.aggregateActionText
                        onActivated: Core.toggleAll()
                    }

                    KCard {
                        Layout.fillWidth: true
                        visible: Core.simulated
                        RowLayout {
                            Layout.fillWidth: true
                            spacing: 10
                            KTag {
                                objectName: "homeSimulatedTag"
                                text: "SIMULATED"
                                tagColor: KikimoraTheme.warning
                            }
                            Text {
                                Layout.fillWidth: true
                                text: "FakeCore · revision " + Core.revision
                                color: KikimoraTheme.textSecondary
                                font.pixelSize: 11
                                wrapMode: Text.WordWrap
                            }
                            KButton {
                                id: nextDemoButton
                                objectName: "nextDemoButton"
                                text: "Next demo state"
                                onClicked: Core.nextDemoScenario()
                            }
                        }
                    }

                    Text {
                        Layout.minimumHeight: 18
                        Layout.preferredHeight: 18
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
                                anchors.rightMargin: 0
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

                    // Entry point to the create flow: a compact tile under the role list.
                    // Add-tile: a flat plus on the same background as the rows, not a button.
                    Rectangle {
                        objectName: "openTunnelWizardButton"
                        Layout.fillWidth: true
                        Layout.preferredHeight: 52
                        radius: KikimoraTheme.radiusSmall
                        color: KikimoraTheme.surface
                        Accessible.role: Accessible.Button
                        Accessible.name: "Добавить туннель"

                        Text {
                            anchors.centerIn: parent
                            text: "+"
                            color: KikimoraTheme.textSecondary
                            font.pixelSize: 26
                        }

                        MouseArea {
                            anchors.fill: parent
                            onClicked: window.selectedTab = 1
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
                            text: "Профиль — это набор ролей. Нажмите на профиль: он включится, а роли выедут снизу."
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

                        Repeater {
                            model: Core.profiles

                            delegate: Rectangle {
                                required property string modelData
                                Layout.fillWidth: true
                                Layout.preferredHeight: 52
                                radius: KikimoraTheme.radiusSmall
                                color: Core.activeProfile === modelData ? KikimoraTheme.surfaceHover : KikimoraTheme.surface
                                border.width: 1
                                border.color: Core.activeProfile === modelData ? KikimoraTheme.accentPrimary : KikimoraTheme.borderSoft

                                RowLayout {
                                    anchors.fill: parent
                                    anchors.leftMargin: 12
                                    anchors.rightMargin: 12
                                    spacing: 8

                                    Text {
                                        Layout.fillWidth: true
                                        text: modelData
                                        color: KikimoraTheme.textPrimary
                                        font.pixelSize: 14
                                        font.weight: Font.DemiBold
                                    }
                                    KTag {
                                        visible: Core.activeProfile === modelData
                                        text: "АКТИВНЫЙ"
                                        tagColor: KikimoraTheme.accentPrimary
                                    }
                                    Text {
                                        text: "\u203a"
                                        color: KikimoraTheme.textSecondary
                                        font.pixelSize: 18
                                    }
                                }

                                MouseArea {
                                    anchors.fill: parent
                                    onClicked: {
                                        if (Core.activeProfile !== modelData)
                                            Core.setActiveProfile(modelData)
                                        profileDrawer.profileName = modelData
                                        profileDrawer.open()
                                    }
                                }
                            }
                        }

                        // Flat tile, same geometry as a profile row.
                        Rectangle {
                            objectName: "openTunnelImportButton"
                            Layout.fillWidth: true
                            Layout.preferredHeight: 48
                            radius: KikimoraTheme.radiusSmall
                            color: KikimoraTheme.surface
                            Accessible.role: Accessible.Button
                            Accessible.name: "Добавить туннель"

                            Text {
                                anchors.centerIn: parent
                                text: "+"
                                color: KikimoraTheme.textSecondary
                                font.pixelSize: 24
                            }

                            MouseArea {
                                anchors.fill: parent
                                onClicked: importPanel.visible = !importPanel.visible
                            }
                        }

                        ColumnLayout {
                            id: importPanel
                            objectName: "importPanel"
                            Layout.fillWidth: true
                            spacing: KikimoraTheme.spacing
                            visible: false

                            Text {
                                Layout.fillWidth: true
                                text: "Вставьте ссылку vless://…, адрес шлюза OpenConnect (https://…) или JSON-конфиг туннеля."
                                color: KikimoraTheme.textSecondary
                                font.pixelSize: 12
                                wrapMode: Text.WordWrap
                            }

                            TextArea {
                                id: importField
                                objectName: "importField"
                                Layout.fillWidth: true
                                Layout.preferredHeight: 110
                                placeholderText: "vless://…   https://…   { … }"
                                wrapMode: TextArea.Wrap
                            }

                            RowLayout {
                                Layout.fillWidth: true
                                spacing: 8

                                KButton {
                                    objectName: "pasteImportButton"
                                    Layout.fillWidth: true
                                    Layout.preferredHeight: 40
                                    text: "Вставить из буфера"
                                    onClicked: importField.text = Desktop.clipboardText()
                                }
                                KButton {
                                    objectName: "addTunnelButton"
                                    Layout.fillWidth: true
                                    Layout.preferredHeight: 40
                                    text: "Добавить"
                                    primary: true
                                    onClicked: Core.importTunnel(importField.text)
                                }
                            }

                            Text {
                                objectName: "importHint"
                                Layout.fillWidth: true
                                visible: text.length > 0
                                text: importField.text.length > 0 ? Core.describeTunnelImport(importField.text) : ""
                                color: KikimoraTheme.textSecondary
                                font.pixelSize: 12
                                wrapMode: Text.WordWrap
                            }
                        }

                        // Same flat tile for a new profile.
                        Rectangle {
                            objectName: "openProfileEditorButton"
                            Layout.fillWidth: true
                            Layout.preferredHeight: 52
                            radius: KikimoraTheme.radiusSmall
                            color: KikimoraTheme.surface
                            Accessible.role: Accessible.Button
                            Accessible.name: "Новый профиль"

                            Text {
                                anchors.centerIn: parent
                                text: "+"
                                color: KikimoraTheme.textSecondary
                                font.pixelSize: 26
                            }

                            MouseArea {
                                anchors.fill: parent
                                onClicked: profileEditor.visible = !profileEditor.visible
                            }
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
                objectName: "routingPage"
                property string titleText: "Routing"
                Flickable {
                    anchors.fill: parent
                    contentHeight: routingColumn.implicitHeight + 28
                    clip: true
                    ColumnLayout {
                        id: routingColumn
                        x: 14
                        width: parent.width - 28

                        Text {
                            Layout.fillWidth: true
                            text: Core.leshySupported
                                  ? "Leshy owns routing, parking and DNS policy for the active roles."
                                  : "Leshy is not available on this platform. Windows currently runs the simulated core only."
                            color: KikimoraTheme.textSecondary
                            font.pixelSize: 13
                            wrapMode: Text.WordWrap
                        }

                        KCard {
                            Layout.fillWidth: true
                            KSectionHeader { text: "Underlay" }
                            KInfoRow { label: "Summary"; value: Core.underlaySummary || "—" }
                            KInfoRow {
                                label: "Epoch"
                                value: (Core.underlay && Core.underlay.epoch !== undefined)
                                       ? String(Core.underlay.epoch) : "—"
                            }
                            KInfoRow {
                                label: "Available"
                                value: (Core.underlay && Core.underlay.available) ? "yes" : "no"
                            }
                        }

                        KSectionHeader { text: "Leshy publication per role" }

                        Repeater {
                            model: Core.roles
                            delegate: KCard {
                                id: page
                                required property string label
                                required property string interfaceName
                                required property string leshyZone
                                required property bool parkingActive
                                required property int parkedRoutes
                                required property bool routeReady
                                required property string endpointState
                                required property var configuredEndpoints
                                required property var liveEndpoints

                                Layout.fillWidth: true
                                KSectionHeader { text: page.label }
                                KInfoRow { label: "Interface"; value: page.interfaceName || "—" }
                                KInfoRow { label: "Zone"; value: page.leshyZone || "—" }
                                KInfoRow { label: "Route ready"; value: page.routeReady ? "yes" : "no" }
                                KInfoRow {
                                    label: "Parking"
                                    value: page.parkingActive ? ("active · " + page.parkedRoutes + " routes")
                                                              : ("inactive · " + page.parkedRoutes + " parked")
                                }
                                KInfoRow { label: "Endpoint"; value: page.endpointState || "—" }
                                KInfoRow {
                                    label: "Configured"
                                    value: page.configuredEndpoints.length > 0
                                           ? page.configuredEndpoints.join(", ") : "—"
                                }
                                KInfoRow {
                                    label: "Live"
                                    value: page.liveEndpoints.length > 0
                                           ? page.liveEndpoints.join(", ") : "—"
                                }
                            }
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

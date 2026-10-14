import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "Controls"
import "Drawers"
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

                    Item {
                        objectName: "connectArea"
                        Layout.fillWidth: true
                        Layout.minimumHeight: 210
                        Layout.preferredHeight: 210
                        Layout.maximumHeight: 210
                            Item {
                                objectName: "connectCircleHost"
                                anchors.fill: parent
                                ConnectCircle {
                                    objectName: "connectCircle"
                                    anchors.centerIn: parent
                                state: Core.aggregateState
                                actionText: Core.aggregateActionText
                                stateText: Core.aggregateStateText
                                onClicked: Core.toggleAll()
                            }
                        }
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

                            RoleRow {
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
                }
            }

            Item {
                property string titleText: "Profiles"
                ColumnLayout {
                    anchors.fill: parent
                    anchors.margins: 14
                    spacing: KikimoraTheme.spacing

                    KPageTitle {
                        text: "Активный профиль"
                    }

                    Text {
                        text: "Выберите профиль, чтобы включить только его роли."
                        color: KikimoraTheme.textSecondary
                        font.pixelSize: 13
                        wrapMode: Text.WordWrap
                        Layout.fillWidth: true
                    }

                    ComboBox {
                        id: profileCombo
                        objectName: "profileCombo"
                        Layout.fillWidth: true
                        model: Core.profiles
                        textRole: "modelData"
                        currentIndex: Core.profiles.indexOf(Core.activeProfile)
                        onActivated: function(index) {
                            Core.setActiveProfile(Core.profiles[index])
                        }
                    }

                    KCard {
                        Layout.fillWidth: true
                        KInfoRow { label: "Текущий профиль"; value: Core.activeProfile || "—" }
                        KInfoRow { label: "Профилей"; value: String(Core.profiles.length) }
                    }

                    Item { Layout.fillHeight: true }
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
                        spacing: KikimoraTheme.spacing

                        KPageTitle { text: routingPage.titleText }

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
                        spacing: KikimoraTheme.spacing

                        KPageTitle { text: settingsPage.titleText }

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
                            KInfoRow {
                                label: "Last command error"
                                value: Core.lastCommandError.length > 0 ? Core.lastCommandError : "—"
                            }
                        }

                        KCard {
                            Layout.fillWidth: true
                            KSectionHeader { text: "Desktop" }
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

        RowLayout {
            Layout.fillWidth: true
            Layout.minimumHeight: 48
            Layout.preferredHeight: 48
            Layout.maximumHeight: 48
            spacing: 6
            Repeater {
                model: [
                    { label: "Главная", iconName: "home" },
                    { label: "Профили", iconName: "profiles" },
                    { label: "Маршруты", iconName: "routing" },
                    { label: "Настройки", iconName: "settings" }
                ]
                delegate: Button {
                    id: tabButton
                    objectName: "bottomTabButton"
                    required property int index
                    required property var modelData
                    Layout.fillWidth: true
                    Layout.minimumHeight: 48
                    Layout.preferredHeight: 48
                    Layout.maximumHeight: 48
                    text: modelData.label
                    onClicked: window.selectedTab = index
                    ToolTip.visible: hovered
                    ToolTip.text: tabButton.text
                    ToolTip.delay: 500
                    background: Rectangle {
                        radius: 14
                        color: window.selectedTab === tabButton.index ? KikimoraTheme.surfaceHover : "transparent"
                    }
                    contentItem: Item {
                        Image {
                            objectName: "tabIcon"
                            anchors.centerIn: parent
                            width: 24
                            height: 24
                            source: "image://system/" + tabButton.modelData.iconName
                            sourceSize: Qt.size(24, 24)
                            smooth: true
                        }
                    }
                }
            }
        }
    }

    RoleDrawer {
        id: roleDrawer
        parent: Overlay.overlay
        onRoleActionRequested: function(index, action) { Core.roleActionByName(index, action) }
    }
}

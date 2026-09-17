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

                    Rectangle {
                        Layout.fillWidth: true
                        Layout.minimumHeight: 40
                        Layout.preferredHeight: 40
                        Layout.maximumHeight: 40
                        visible: Core.backendKind === "fake"
                        radius: KikimoraTheme.radiusSmall
                        color: KikimoraTheme.surface
                        RowLayout {
                            id: demoRow
                            anchors.fill: parent
                            anchors.margins: 10
                            Text {
                                Layout.fillWidth: true
                                text: "FakeCore · revision " + Core.revision
                                color: KikimoraTheme.textSecondary
                                font.pixelSize: 11
                            }
                            Button {
                                id: nextDemoButton
                                objectName: "nextDemoButton"
                                text: "Next demo state"
                                onClicked: Core.nextDemoScenario()
                                background: Rectangle {
                                    radius: 8
                                    color: nextDemoButton.hovered ? KikimoraTheme.surfaceHover : KikimoraTheme.surface
                                }
                                contentItem: Text {
                                    text: nextDemoButton.text
                                    color: KikimoraTheme.textPrimary
                                    font.pixelSize: 11
                                    horizontalAlignment: Text.AlignHCenter
                                    verticalAlignment: Text.AlignVCenter
                                }
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

            PlaceholderPage {
                titleText: "Profiles"
                bodyText: "Profile editing will bind to the future core API.\nThe prototype keeps the information architecture clickable."
            }
            PlaceholderPage {
                titleText: "Routing"
                bodyText: Core.leshySupported
                          ? "Leshy routing / parking / endpoint safety will appear here."
                          : "Leshy is not available on this platform. Windows currently stays on FakeCore."
            }
            PlaceholderPage {
                titleText: "Settings"
                bodyText: "UI preferences, diagnostics and desktop integration will be added here."
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
        onRoleActionRequested: function(index) { Core.roleAction(index) }
    }

    component PlaceholderPage: Item {
        property string titleText
        property string bodyText
        ColumnLayout {
            anchors.centerIn: parent
            width: Math.min(parent.width - 40, 340)
            spacing: 12
            Text {
                Layout.fillWidth: true
                text: titleText
                color: KikimoraTheme.textPrimary
                font.pixelSize: 26
                font.weight: Font.Bold
                horizontalAlignment: Text.AlignHCenter
            }
            Text {
                Layout.fillWidth: true
                text: bodyText
                color: KikimoraTheme.textSecondary
                font.pixelSize: 13
                wrapMode: Text.WordWrap
                horizontalAlignment: Text.AlignHCenter
            }
        }
    }
}

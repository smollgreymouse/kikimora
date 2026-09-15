import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "Controls"
import "Drawers"
import "Theme"

ApplicationWindow {
    id: window
    visible: true
    width: 460
    height: 760
    minimumWidth: 390
    minimumHeight: 650
    title: "Kikimora"
    color: KikimoraTheme.background

    property int selectedTab: 0

    ColumnLayout {
        anchors.fill: parent
        anchors.margins: 18
        spacing: 14

        RowLayout {
            Layout.fillWidth: true
            Text {
                text: "Kikimora"
                color: KikimoraTheme.textPrimary
                font.pixelSize: 22
                font.weight: Font.Bold
            }
            Item { Layout.fillWidth: true }
            Text {
                text: Core.activeProfile
                color: KikimoraTheme.accent
                font.pixelSize: 13
            }
        }

        StackLayout {
            Layout.fillWidth: true
            Layout.fillHeight: true
            currentIndex: window.selectedTab

            Item {
                ColumnLayout {
                    anchors.fill: parent
                    spacing: 14

                    Text {
                        Layout.alignment: Qt.AlignHCenter
                        text: Core.underlaySummary
                        color: KikimoraTheme.textSecondary
                        font.pixelSize: 12
                    }

                    Item {
                        Layout.fillWidth: true
                        Layout.preferredHeight: 220
                        ConnectCircle {
                            anchors.centerIn: parent
                            state: Core.aggregateState
                            actionText: Core.aggregateActionText
                            stateText: Core.aggregateStateText
                            onClicked: Core.toggleAll()
                        }
                    }

                    Rectangle {
                        Layout.fillWidth: true
                        implicitHeight: demoRow.implicitHeight + 20
                        radius: KikimoraTheme.radiusSmall
                        color: "#121216"
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
                        text: "VPN roles"
                        color: KikimoraTheme.textSecondary
                        font.pixelSize: 12
                    }

                    ListView {
                        id: roleList
                        Layout.fillWidth: true
                        Layout.fillHeight: true
                        spacing: 8
                        clip: true
                        model: Core.roles

                        delegate: RoleRow {
                            required property int index
                            required property string label
                            required property string protocol
                            required property string server
                            required property string state
                            required property string stateText

                            width: ListView.view.width
                            onOpenRequested: {
                                roleDrawer.roleIndex = index
                                roleDrawer.roleData = Core.roles.get(index)
                                roleDrawer.open()
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
            spacing: 6
            Repeater {
                model: ["Home", "Profiles", "Routing", "Settings"]
                delegate: Button {
                    id: tabButton
                    required property int index
                    required property string modelData
                    Layout.fillWidth: true
                    text: modelData
                    onClicked: window.selectedTab = index
                    background: Rectangle {
                        radius: 10
                        color: window.selectedTab === tabButton.index ? KikimoraTheme.surfaceHover : "transparent"
                    }
                    contentItem: Text {
                        text: tabButton.text
                        color: window.selectedTab === tabButton.index ? KikimoraTheme.accent : KikimoraTheme.textSecondary
                        horizontalAlignment: Text.AlignHCenter
                        verticalAlignment: Text.AlignVCenter
                        font.pixelSize: 12
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

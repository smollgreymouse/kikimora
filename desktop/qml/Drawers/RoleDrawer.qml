import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "../Theme"

Drawer {
    id: root
    edge: Qt.BottomEdge
    width: parent ? parent.width : 440
    height: parent ? Math.min(parent.height * 0.72, 540) : 500

    property int roleIndex: -1
    property var roleData: ({})
    signal roleActionRequested(int roleIndex)

    background: Rectangle {
        color: KikimoraTheme.surface
        radius: KikimoraTheme.radiusLarge
    }

    contentItem: Flickable {
        contentHeight: details.implicitHeight + 40
        clip: true

        ColumnLayout {
            id: details
            width: parent.width
            spacing: 14
            anchors.margins: 20

            Rectangle {
                Layout.alignment: Qt.AlignHCenter
                Layout.preferredWidth: 34
                Layout.preferredHeight: 4
                radius: 2
                color: KikimoraTheme.border
            }

            Text {
                text: root.roleData.label || "VPN role"
                color: KikimoraTheme.textPrimary
                font.pixelSize: 24
                font.weight: Font.Bold
            }
            Text {
                text: (root.roleData.protocol || "") + " · " + (root.roleData.server || "")
                color: KikimoraTheme.textSecondary
                font.pixelSize: 13
            }

            Repeater {
                model: [
                    ["State", root.roleData.stateText || "—"],
                    ["Interface", root.roleData.interfaceName || "—"],
                    ["Physical underlay", root.roleData.underlay || "—"],
                    ["Endpoint path", root.roleData.endpointState || "—"],
                    ["Leshy publication", root.roleData.leshyPublished ? "Published" : "Withdrawn"],
                    ["Parked routes", String(root.roleData.parkedRoutes ?? 0)],
                    ["Last recovery", root.roleData.lastRecovery || "—"],
                    ["Last error", root.roleData.lastError || "—"]
                ]

                delegate: RowLayout {
                    required property var modelData
                    Layout.fillWidth: true
                    Text {
                        text: modelData[0]
                        color: KikimoraTheme.textSecondary
                        font.pixelSize: 13
                        Layout.preferredWidth: 132
                    }
                    Text {
                        text: modelData[1]
                        color: KikimoraTheme.textPrimary
                        font.pixelSize: 13
                        wrapMode: Text.WordWrap
                        Layout.fillWidth: true
                    }
                }
            }

            Button {
                Layout.fillWidth: true
                Layout.topMargin: 10
                text: root.roleData.state === "Stopped" ? "Connect this role" : "Disconnect this role"
                onClicked: {
                    root.roleActionRequested(root.roleIndex)
                    root.close()
                }
                background: Rectangle {
                    radius: KikimoraTheme.radiusSmall
                    color: parent.hovered ? KikimoraTheme.surfaceHover : "transparent"
                    border.width: 1
                    border.color: KikimoraTheme.border
                }
                contentItem: Text {
                    text: parent.text
                    color: KikimoraTheme.textPrimary
                    horizontalAlignment: Text.AlignHCenter
                    verticalAlignment: Text.AlignVCenter
                    font.pixelSize: 14
                }
            }
        }
    }
}

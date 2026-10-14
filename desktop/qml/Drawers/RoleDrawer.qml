import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "../Theme"
import "../Controls"

Drawer {
    id: root
    objectName: "roleDrawer"
    edge: Qt.BottomEdge
    width: parent ? parent.width : 440
    height: parent ? Math.min(parent.height * 0.72, 540) : 500

    property int roleIndex: -1
    property var roleData: ({})
    // Actions are reported by the core; the UI must not guess them from state.
    property var actions: (roleData && roleData.availableActions) ? roleData.availableActions : []
    signal roleActionRequested(int roleIndex, string action)

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

    background: Rectangle {
        color: KikimoraTheme.surface
        radius: KikimoraTheme.radiusLarge
    }

    contentItem: Flickable {
        contentHeight: details.implicitHeight + 40
        clip: true

        ColumnLayout {
            id: details
            x: 20
            width: parent.width - 40
            spacing: 14

            Rectangle {
                Layout.alignment: Qt.AlignHCenter
                Layout.preferredWidth: 34
                Layout.preferredHeight: 4
                radius: 2
                color: KikimoraTheme.border
            }

            Text {
                objectName: "drawerRoleTitle"
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
                    ["Parked routes", String(root.roleData.parkedRoutes !== undefined ? root.roleData.parkedRoutes : 0)],
                    ["Route ready", root.roleData.routeReady ? "yes" : "no"],
                    ["Last recovery", root.roleData.lastRecovery || "—"],
                    ["Last error", root.roleData.lastError || "—"]
                ]

                delegate: KInfoRow {
                    required property var modelData
                    Layout.fillWidth: true
                    label: modelData[0]
                    value: modelData[1]
                }
            }

            KButton {
                objectName: "roleActionButton"
                Layout.fillWidth: true
                Layout.topMargin: 10
                visible: root.actions.length > 0
                primary: true
                text: root.actions.length > 0 ? root.actionLabel(root.actions[0]) : ""
                onClicked: {
                    root.roleActionRequested(root.roleIndex, root.actions[0])
                    root.close()
                }
            }

            Repeater {
                model: root.actions.slice(1)
                delegate: KButton {
                    objectName: "roleSecondaryActionButton"
                    required property string modelData
                    Layout.fillWidth: true
                    text: root.actionLabel(modelData)
                    onClicked: {
                        root.roleActionRequested(root.roleIndex, modelData)
                        root.close()
                    }
                }
            }
        }
    }
}

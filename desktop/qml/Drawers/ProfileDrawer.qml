import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "../Controls"
import "../Theme"

// Roles of one profile, in the same bottom slide-out the role detail uses.
KDrawer {
    id: root
    objectName: "profileDrawer"

    property string profileName: ""
    property var roles: null
    property var profiles: []

    signal roleMoveRequested(string role, string profile)

    KSectionHeader { text: "Роли профиля " + root.profileName }

    Repeater {
        model: root.roles

        delegate: RowLayout {
            required property string label
            required property string state
            required property string stateText
            property string roleLabel: label

            Layout.fillWidth: true
            spacing: 10

            Text {
                Layout.fillWidth: true
                text: label
                color: KikimoraTheme.textPrimary
                font.pixelSize: 13
            }

            KStatusChip { state: state; text: stateText }

            Text {
                objectName: "profileRoleKebab_" + label
                text: "\u22ee"
                color: KikimoraTheme.textSecondary
                font.pixelSize: 16

                MouseArea {
                    anchors.fill: parent
                    anchors.margins: -8
                    onClicked: roleMoveMenu.popup()
                }

                Menu {
                    id: roleMoveMenu

                    Repeater {
                        model: root.profiles

                        delegate: MenuItem {
                            required property string modelData
                            text: "Переместить в " + modelData
                            enabled: String(modelData) !== String(root.profileName)
                            onTriggered: root.roleMoveRequested(roleLabel, modelData)
                        }
                    }
                }
            }
        }
    }

    Text {
        Layout.fillWidth: true
        visible: root.roles ? root.roles.rowCount() === 0 : false
        text: "В этом профиле пока нет ролей."
        color: KikimoraTheme.textSecondary
        font.pixelSize: 12
        wrapMode: Text.WordWrap
    }
}

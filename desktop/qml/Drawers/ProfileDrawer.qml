import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "../Controls"
import "../Theme"

// Roles of one profile plus the tunnel import, in the same bottom slide-out the role detail uses.
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
                elide: Text.ElideRight
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

    // The import lives in the drawer: the page keeps a single plus, for a new profile.
    KSectionHeader { text: "Добавить туннель" }

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
        Layout.preferredHeight: 100
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

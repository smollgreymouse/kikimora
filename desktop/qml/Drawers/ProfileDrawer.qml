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
    signal roleSelected(string role)

    KSectionHeader { text: "Роли профиля " + root.profileName }

    // One shared role list: the sheet only narrows it to its own profile.
    KRoleList {
        Layout.fillWidth: true
        implicitHeight: 240
        rowName: "profileRoleRow"
        profile: root.profileName
        expandable: true
        onRoleActivated: function(label) {
            root.roleSelected(label)
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
        color: KikimoraTheme.textPrimary
        placeholderTextColor: KikimoraTheme.textSecondary
        selectionColor: KikimoraTheme.accent
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

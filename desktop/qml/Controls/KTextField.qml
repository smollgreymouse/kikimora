import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "../Theme"

// Labelled field. Read-only by default because most product values are core
// state, not inputs; editable mode is available for future settings.
ColumnLayout {
    id: root
    property string label: ""
    property string value: ""
    property bool readOnly: true
    property alias echoMode: field.echoMode
    signal edited(string value)

    spacing: 4
    Layout.fillWidth: true

    Text {
        text: root.label
        color: KikimoraTheme.textSecondary
        font.pixelSize: 11
    }
    TextField {
        id: field
        objectName: "kTextFieldInput"
        Layout.fillWidth: true
        text: root.value
        readOnly: root.readOnly
        selectByMouse: true
        Accessible.name: root.label
        onEditingFinished: root.edited(text)
        background: Rectangle {
            radius: KikimoraTheme.radiusSmall
            color: KikimoraTheme.surfaceBase
            border.width: field.activeFocus ? KikimoraTheme.focusRingWidth : 1
            border.color: field.activeFocus ? KikimoraTheme.accentPrimary : KikimoraTheme.borderSoft
        }
    }
}

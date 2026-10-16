import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "../Theme"

RowLayout {
    id: root
    property string label: ""
    property bool checked: false
    signal toggled(bool checked)

    spacing: 10
    Accessible.role: Accessible.CheckBox
    Accessible.name: root.label

    Text {
        Layout.fillWidth: true
        text: root.label
        color: KikimoraTheme.textPrimary
        font.pixelSize: 13
        wrapMode: Text.WordWrap
    }
    Switch {
        id: control
        checked: root.checked
        Accessible.name: root.label
        onToggled: root.toggled(checked)
    }
}

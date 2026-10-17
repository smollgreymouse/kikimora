import QtQuick
import QtQuick.Layouts
import "../Theme"

RowLayout {
    id: root
    property string label: ""
    property string value: ""
    property int labelWidth: 132

    Layout.fillWidth: true
    spacing: 10

    Text {
        text: root.label
        color: KikimoraTheme.textSecondary
        font.pixelSize: 13
        Layout.preferredWidth: root.labelWidth
        Layout.alignment: Qt.AlignTop
    }
    Text {
        text: root.value
        color: KikimoraTheme.textPrimary
        font.pixelSize: 13
        wrapMode: Text.WordWrap
        Layout.fillWidth: true
    }
}

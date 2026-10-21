import QtQuick
import "../Theme"

Rectangle {
    id: root
    property string text: ""
    property color tagColor: KikimoraTheme.textSecondary

    implicitWidth: label.implicitWidth + 16
    implicitHeight: label.implicitHeight + 6
    radius: 999
    color: Qt.rgba(tagColor.r, tagColor.g, tagColor.b, 0.14)
    border.width: 1
    border.color: Qt.rgba(tagColor.r, tagColor.g, tagColor.b, 0.35)

    Text {
        id: label
        anchors.centerIn: parent
        text: root.text
        color: root.tagColor
        font.pixelSize: 11
        font.weight: Font.DemiBold
    }
}

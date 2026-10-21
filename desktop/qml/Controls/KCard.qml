import QtQuick
import QtQuick.Layouts
import "../Theme"

Rectangle {
    id: root
    default property alias content: layout.data

    radius: KikimoraTheme.radiusLarge
    color: KikimoraTheme.surface
    border.width: 1
    border.color: KikimoraTheme.border
    implicitWidth: layout.implicitWidth + 24
    implicitHeight: layout.implicitHeight + 24

    ColumnLayout {
        id: layout
        anchors.fill: parent
        anchors.margins: 12
        spacing: 8
    }
}

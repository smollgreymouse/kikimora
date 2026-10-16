import QtQuick
import QtQuick.Layouts
import "../Theme"

// Page header: title, optional subtitle and a trailing slot for page actions.
Item {
    id: root
    property string title: ""
    property string subtitle: ""
    default property alias trailing: trailingRow.data

    implicitHeight: headerColumn.implicitHeight
    Layout.fillWidth: true

    ColumnLayout {
        id: headerColumn
        anchors.fill: parent
        spacing: 2
        Text {
            objectName: "pageHeaderTitle"
            text: root.title
            color: KikimoraTheme.textPrimary
            font.pixelSize: 22
            font.weight: Font.Bold
            Accessible.role: Accessible.Heading
            Accessible.name: root.title
        }
        Text {
            visible: root.subtitle.length > 0
            text: root.subtitle
            color: KikimoraTheme.textSecondary
            font.pixelSize: 12
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }
        RowLayout {
            id: trailingRow
            Layout.fillWidth: true
            spacing: 8
        }
    }
}

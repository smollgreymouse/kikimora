import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "../Theme"

// Themed bottom drawer shell: drag handle, scrollable content column and the
// standard close policies (Escape and outside press).
Drawer {
    id: root
    edge: Qt.BottomEdge
    width: parent ? parent.width : 440
    height: parent ? Math.min(parent.height * 0.78, 620) : 520
    default property alias contents: contentColumn.data

    background: Rectangle {
        color: KikimoraTheme.surfaceBase
        radius: KikimoraTheme.radiusLarge
        border.width: 1
        border.color: KikimoraTheme.borderSoft
    }

    contentItem: Flickable {
        id: flickable
        contentHeight: contentColumn.implicitHeight + 40
        clip: true
        Accessible.role: Accessible.Pane
        Accessible.name: "Role details"

        ColumnLayout {
            id: contentColumn
            x: 20
            width: flickable.width - 40
            spacing: 14

            Rectangle {
                Layout.alignment: Qt.AlignHCenter
                Layout.preferredWidth: 34
                Layout.preferredHeight: 4
                radius: 2
                color: KikimoraTheme.borderSoft
                Accessible.ignored: true
            }
        }
    }
}

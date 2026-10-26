import QtQuick
import QtQuick.Layouts
import "../Theme"

// A compact checkbox row: the whole line toggles, the box shows the state. The parent owns
// the value (no internal state), so a rebuilt delegate cannot leave a stale selection on
// screen - which is exactly what KSwitch does once its inner control is touched.
Item {
    id: root
    property string label: ""
    property bool checked: false
    signal toggled(bool checked)

    implicitHeight: 34
    Layout.fillWidth: true
    Accessible.role: Accessible.CheckBox
    Accessible.name: root.label
    Accessible.checkable: true
    Accessible.checked: root.checked

    RowLayout {
        anchors.fill: parent
        spacing: 10

        Rectangle {
            Layout.preferredWidth: 20
            Layout.preferredHeight: 20
            radius: 6
            color: root.checked ? KikimoraTheme.accent : "transparent"
            border.width: root.checked ? 0 : 1
            border.color: KikimoraTheme.border

            Text {
                anchors.centerIn: parent
                visible: root.checked
                text: "\u2713"
                color: KikimoraTheme.accentText
                font.pixelSize: 12
                font.weight: Font.DemiBold
            }
        }

        Text {
            Layout.fillWidth: true
            text: root.label
            color: KikimoraTheme.textPrimary
            font.pixelSize: 13
            elide: Text.ElideRight
        }
    }

    MouseArea {
        anchors.fill: parent
        cursorShape: Qt.PointingHandCursor
        onClicked: root.toggled(!root.checked)
    }
}

import QtQuick
import "../Theme"

// State chip. The text is always rendered, so a role state never depends on
// color perception alone (accessibility contract in the plan's U13).
Rectangle {
    id: root
    property string text: ""
    property string state: ""

    readonly property color tone: {
        switch (state) {
        case "Ready": return KikimoraTheme.accentSuccess
        case "Recovering":
        case "Validating":
        case "Starting":
        case "Connecting": return KikimoraTheme.accentWarning
        case "Failed": return KikimoraTheme.accentError
        default: return KikimoraTheme.borderSoft
        }
    }
    readonly property string label: text.length > 0 ? text : state

    implicitWidth: chipLabel.implicitWidth + 20
    implicitHeight: chipLabel.implicitHeight + 8
    radius: implicitHeight / 2
    color: Qt.rgba(tone.r, tone.g, tone.b, 0.18)
    border.width: 1
    border.color: tone
    Accessible.role: Accessible.StaticText
    Accessible.name: label

    Text {
        id: chipLabel
        anchors.centerIn: parent
        text: root.label
        color: KikimoraTheme.textPrimary
        font.pixelSize: 12
    }
}

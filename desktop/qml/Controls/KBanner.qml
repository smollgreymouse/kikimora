import QtQuick
import QtQuick.Layouts
import "../Theme"

// Inline banner for platform/simulation warnings and non-blocking errors.
Rectangle {
    id: root
    property string text: ""
    property string tone: "info"      // info | warning | error
    property string actionText: ""
    signal actionTriggered()

    readonly property color toneColor: {
        switch (tone) {
        case "warning": return KikimoraTheme.accentWarning
        case "error": return KikimoraTheme.accentError
        default: return KikimoraTheme.accentPrimary
        }
    }
    implicitHeight: bannerRow.implicitHeight + 20
    radius: KikimoraTheme.radiusSmall
    color: Qt.rgba(toneColor.r, toneColor.g, toneColor.b, 0.14)
    border.width: 1
    border.color: toneColor
    Accessible.role: Accessible.AlertMessage
    Accessible.name: text

    RowLayout {
        id: bannerRow
        anchors.fill: parent
        anchors.margins: 10
        spacing: 10
        Text {
            Layout.fillWidth: true
            text: root.text
            color: KikimoraTheme.textPrimary
            font.pixelSize: 12
            wrapMode: Text.WordWrap
        }
        KButton {
            visible: root.actionText.length > 0
            text: root.actionText
            onClicked: root.actionTriggered()
        }
    }
}

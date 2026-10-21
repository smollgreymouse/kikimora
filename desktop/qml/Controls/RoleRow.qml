import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "../Theme"

Button {
    id: root
    objectName: "roleRow"
    required property string label
    required property string protocol
    required property string server
    required property string state
    required property string stateText

    signal openRequested()

    implicitHeight: 70
    hoverEnabled: true
    leftPadding: 14
    rightPadding: 12
    topPadding: 10
    bottomPadding: 10
    onClicked: openRequested()

    background: Rectangle {
        radius: KikimoraTheme.radiusSmall
        color: root.down ? KikimoraTheme.surfacePressed
                         : (root.hovered ? KikimoraTheme.surfaceHover : KikimoraTheme.surfaceRaised)
        border.width: root.activeFocus ? KikimoraTheme.focusRingWidth : 0
        border.color: KikimoraTheme.accentPrimary
        Behavior on color { ColorAnimation { duration: KikimoraTheme.animationFast } }
    }

    contentItem: RowLayout {
        spacing: 10

        ColumnLayout {
            Layout.fillWidth: true
            spacing: 3

            Text {
                text: root.label
                color: KikimoraTheme.textPrimary
                font.pixelSize: 15
                font.weight: Font.DemiBold
                elide: Text.ElideRight
                Layout.fillWidth: true
            }

            Text {
                text: root.protocol + " · " + root.server
                color: KikimoraTheme.textSecondary
                font.pixelSize: 11
                elide: Text.ElideRight
                Layout.fillWidth: true
            }
        }

        Text {
            text: root.stateText
            visible: root.state !== "Connecting" && root.state !== "Recovering"
                     && root.state !== "Starting" && root.state !== "Validating"
            color: root.state === "Ready" ? KikimoraTheme.success
                   : root.state === "Failed" || root.state === "Error" ? KikimoraTheme.error
                   : KikimoraTheme.textSecondary
            font.pixelSize: 11
            font.weight: root.state === "Ready" ? Font.DemiBold : Font.Normal
        }

        Text {
            text: "\u203a"
            color: KikimoraTheme.textMuted
            font.pixelSize: 22
        }
    }
}

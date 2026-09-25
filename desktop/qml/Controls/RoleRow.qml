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

    implicitHeight: 68
    hoverEnabled: true
    onClicked: openRequested()

    background: Rectangle {
        radius: KikimoraTheme.radiusSmall
        color: root.hovered ? KikimoraTheme.surfaceHover : KikimoraTheme.surface
        border.width: root.activeFocus ? 1 : 0
        border.color: KikimoraTheme.accentHover
        Behavior on color { ColorAnimation { duration: KikimoraTheme.animationFast } }
    }

    contentItem: RowLayout {
        spacing: 12

        Rectangle {
            Layout.preferredWidth: 9
            Layout.preferredHeight: 9
            radius: 5
            color: {
                if (root.state === "Ready") return KikimoraTheme.success
                if (root.state === "Failed") return KikimoraTheme.error
                if (root.state === "Recovering" || root.state === "WaitingForUnderlay") return KikimoraTheme.warning
                return KikimoraTheme.textSecondary
            }
        }

        ColumnLayout {
            Layout.fillWidth: true
            spacing: 2
            Text {
                text: root.label
                color: KikimoraTheme.textPrimary
                font.pixelSize: 15
                font.weight: Font.DemiBold
            }
            Text {
                text: root.protocol + " · " + root.server
                color: KikimoraTheme.textSecondary
                font.pixelSize: 12
                elide: Text.ElideRight
                Layout.fillWidth: true
            }
        }

        Text {
            text: root.stateText
            color: root.state === "Ready" ? KikimoraTheme.success : KikimoraTheme.textSecondary
            font.pixelSize: 12
        }
        Text {
            text: "›"
            color: KikimoraTheme.textSecondary
            font.pixelSize: 24
        }
    }
}

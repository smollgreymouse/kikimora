import QtQuick
import QtQuick.Controls
import QtQuick.Shapes
import "../Theme"

Button {
    id: root
    required property string state
    required property string actionText
    required property string stateText

    implicitWidth: 190
    implicitHeight: 190
    hoverEnabled: true

    contentItem: Column {
        anchors.centerIn: parent
        spacing: 8

        Text {
            anchors.horizontalCenter: parent.horizontalCenter
            text: root.actionText
            color: root.state === "Ready" ? KikimoraTheme.accent : KikimoraTheme.textPrimary
            font.pixelSize: 20
            font.weight: Font.DemiBold
        }
        Text {
            anchors.horizontalCenter: parent.horizontalCenter
            width: 138
            horizontalAlignment: Text.AlignHCenter
            wrapMode: Text.WordWrap
            text: root.stateText
            color: KikimoraTheme.textSecondary
            font.pixelSize: 12
        }
    }

    background: Item {
        Shape {
            id: ring
            anchors.fill: parent
            layer.enabled: true
            layer.samples: 4

            ShapePath {
                fillColor: "transparent"
                strokeColor: {
                    if (root.state === "Ready") return KikimoraTheme.accent
                    if (root.state === "Failed") return KikimoraTheme.error
                    if (root.state === "Recovering" || root.state === "WaitingForUnderlay") return KikimoraTheme.warning
                    return KikimoraTheme.textSecondary
                }
                strokeWidth: root.hovered ? 4 : 3
                capStyle: ShapePath.RoundCap
                PathAngleArc {
                    centerX: ring.width / 2
                    centerY: ring.height / 2
                    radiusX: 91
                    radiusY: 91
                    startAngle: 0
                    sweepAngle: 360
                }
            }
        }

        BusyIndicator {
            anchors.fill: parent
            anchors.margins: 7
            running: root.state === "Connecting" || root.state === "Disconnecting" || root.state === "Recovering"
            visible: running
        }
    }
}

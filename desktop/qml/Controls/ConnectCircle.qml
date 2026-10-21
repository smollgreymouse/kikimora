import QtQuick
import QtQuick.Controls
import QtQuick.Shapes
import "../Theme"

Item {
    id: root
    required property string state
    required property string actionText
    required property string stateText
    signal clicked()

    implicitWidth: 190
    implicitHeight: 190
    property bool hovered: mouseArea.containsMouse

    Column {
        id: content
        anchors.centerIn: parent
        spacing: 8
        width: 190

        Text {
            anchors.horizontalCenter: parent.horizontalCenter
            text: root.actionText
            color: root.state === "Ready" ? KikimoraTheme.success : KikimoraTheme.textPrimary
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

    Shape {
        id: ring
        anchors.fill: parent
        layer.enabled: true
        layer.samples: 4

        ShapePath {
            fillColor: "transparent"
            strokeColor: {
                if (root.state === "Ready") return KikimoraTheme.success
                if (root.state === "Failed") return KikimoraTheme.error
                if (root.state === "Recovering" || root.state === "WaitingForUnderlay") return KikimoraTheme.warning
                return KikimoraTheme.textSecondary
            }
            strokeWidth: root.hovered ? 4 : 3
            capStyle: ShapePath.RoundCap
            PathAngleArc {
                centerX: ring.width / 2
                centerY: ring.height / 2
                radiusX: Math.max(0, ring.width / 2 - 3)
                radiusY: Math.max(0, ring.height / 2 - 3)
                startAngle: 0
                sweepAngle: 360
            }
        }
    }

    // Waiting: the whole ring is the wide band, and a coloured segment runs along it.
    Item {
        id: progress
        // Click-through: the connect button sits under this layer.
        enabled: false
        z: -1
        anchors.fill: parent
        visible: root.state === "Connecting" || root.state === "Disconnecting" || root.state === "Recovering"

        // the band itself
        Shape {
            anchors.fill: parent
            layer.enabled: true
            layer.samples: 4

            ShapePath {
                fillColor: "transparent"
                strokeColor: KikimoraTheme.borderSoft
                strokeWidth: 7
                capStyle: ShapePath.FlatCap
                PathAngleArc {
                    centerX: progress.width / 2
                    centerY: progress.height / 2
                    radiusX: Math.max(0, progress.width / 2 - 4)
                    radiusY: Math.max(0, progress.height / 2 - 4)
                    startAngle: 0
                    sweepAngle: 360
                }
            }
        }

        // the colour running along it
        Shape {
            id: runner
            anchors.fill: parent
            layer.enabled: true
            layer.samples: 4

            RotationAnimator on rotation {
                running: progress.visible && KikimoraTheme.motionEnabled
                from: 0
                to: 360
                duration: 1100
                loops: Animation.Infinite
            }

            ShapePath {
                fillColor: "transparent"
                strokeColor: root.state === "Recovering" ? KikimoraTheme.warning : KikimoraTheme.accentPrimary
                strokeWidth: 7
                capStyle: ShapePath.RoundCap
                PathAngleArc {
                    centerX: runner.width / 2
                    centerY: runner.height / 2
                    radiusX: Math.max(0, runner.width / 2 - 4)
                    radiusY: Math.max(0, runner.height / 2 - 4)
                    startAngle: 0
                    sweepAngle: 84
                }
            }
        }
    }

    MouseArea {
        id: mouseArea
        anchors.fill: parent
        hoverEnabled: true
        onClicked: root.clicked()
    }
}

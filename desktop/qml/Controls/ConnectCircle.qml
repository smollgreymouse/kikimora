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

    implicitWidth: 224
    implicitHeight: 224
    property bool hovered: mouseArea.containsMouse
    // Only transitional operations have something to wait for. "Recovering"
    // is a settled state with background retries: the arc stays visible as a
    // static partial-protection cue, but the endless spinner must not run.
    readonly property bool busy: root.state === "Connecting" || root.state === "Disconnecting"
    property color ringColor: {
        if (root.state === "Ready") return KikimoraTheme.accentPrimary
        if (root.state === "Failed") return KikimoraTheme.error
        if (root.state === "Recovering" || root.state === "WaitingForUnderlay") return KikimoraTheme.warning
        return KikimoraTheme.textMuted
    }

    // Three concentric strokes create the soft halo from the Amnezia connect
    // control without a platform-specific blur effect.
    Shape {
        anchors.fill: parent
        visible: root.state === "Ready" || root.hovered
        opacity: root.hovered ? 1.0 : 0.82
        layer.enabled: true
        layer.samples: 4

        ShapePath {
            fillColor: "transparent"
            strokeColor: Qt.rgba(root.ringColor.r, root.ringColor.g, root.ringColor.b, 0.10)
            strokeWidth: 14
            PathAngleArc {
                centerX: root.width / 2
                centerY: root.height / 2
                radiusX: Math.max(0, root.width / 2 - 10)
                radiusY: Math.max(0, root.height / 2 - 10)
                startAngle: 0
                sweepAngle: 360
            }
        }
        ShapePath {
            fillColor: "transparent"
            strokeColor: Qt.rgba(root.ringColor.r, root.ringColor.g, root.ringColor.b, 0.22)
            strokeWidth: 7
            PathAngleArc {
                centerX: root.width / 2
                centerY: root.height / 2
                radiusX: Math.max(0, root.width / 2 - 10)
                radiusY: Math.max(0, root.height / 2 - 10)
                startAngle: 0
                sweepAngle: 360
            }
        }
    }

    Shape {
        id: ring
        anchors.fill: parent
        layer.enabled: true
        layer.samples: 4

        ShapePath {
            fillColor: "transparent"
            strokeColor: root.ringColor
            strokeWidth: root.hovered ? 3.2 : 2.4
            capStyle: ShapePath.RoundCap
            PathAngleArc {
                centerX: ring.width / 2
                centerY: ring.height / 2
                radiusX: Math.max(0, ring.width / 2 - 10)
                radiusY: Math.max(0, ring.height / 2 - 10)
                startAngle: 0
                sweepAngle: 360
            }
        }
    }

    // Busy states keep the full ring visible and move one warm segment around
    // it; "Recovering" shows the same arc as a static cue without animating.
    Item {
        id: progress
        anchors.fill: parent
        visible: root.busy || root.state === "Recovering"
        enabled: false

        Shape {
            anchors.fill: parent
            layer.enabled: true
            layer.samples: 4
            ShapePath {
                fillColor: "transparent"
                strokeColor: KikimoraTheme.border
                strokeWidth: 7
                PathAngleArc {
                    centerX: progress.width / 2
                    centerY: progress.height / 2
                    radiusX: Math.max(0, progress.width / 2 - 10)
                    radiusY: Math.max(0, progress.height / 2 - 10)
                    startAngle: 0
                    sweepAngle: 360
                }
            }
        }

        Shape {
            id: runner
            anchors.fill: parent
            layer.enabled: true
            layer.samples: 4

            RotationAnimator on rotation {
                running: root.busy && KikimoraTheme.motionEnabled
                from: 0
                to: 360
                duration: 1150
                loops: Animation.Infinite
            }

            ShapePath {
                fillColor: "transparent"
                strokeColor: root.state === "Recovering"
                             ? KikimoraTheme.warning : KikimoraTheme.accentPrimary
                strokeWidth: 7
                capStyle: ShapePath.RoundCap
                PathAngleArc {
                    centerX: runner.width / 2
                    centerY: runner.height / 2
                    radiusX: Math.max(0, runner.width / 2 - 10)
                    radiusY: Math.max(0, runner.height / 2 - 10)
                    startAngle: 0
                    sweepAngle: 78
                }
            }
        }
    }

    Column {
        anchors.centerIn: parent
        spacing: 8
        width: 176

        Text {
            anchors.horizontalCenter: parent.horizontalCenter
            text: root.actionText
            color: root.state === "Ready" ? KikimoraTheme.accentPrimary
                                          : KikimoraTheme.textPrimary
            font.pixelSize: 22
            font.weight: Font.DemiBold
            font.letterSpacing: 0.25
        }
        Text {
            anchors.horizontalCenter: parent.horizontalCenter
            width: 160
            horizontalAlignment: Text.AlignHCenter
            wrapMode: Text.WordWrap
            text: root.stateText
            color: KikimoraTheme.textSecondary
            font.pixelSize: 12
            lineHeight: 1.1
        }
    }

    MouseArea {
        id: mouseArea
        anchors.fill: parent
        hoverEnabled: true
        // The core unreachable: a click would be a silent no-op (commands are
        // dropped while the socket is down), so the control says so instead.
        enabled: Core.coreState !== "Unavailable"
        cursorShape: enabled ? Qt.PointingHandCursor : Qt.ForbiddenCursor
        onClicked: root.clicked()
    }
}

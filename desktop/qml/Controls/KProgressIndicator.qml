import QtQuick
import "../Theme"

// Bounded activity indicator. Reduced-motion users get a static glyph instead of
// a spinner (the theme exposes motionEnabled; the plan's U13 requires it).
Item {
    id: root
    property bool running: false
    property string label: ""
    implicitWidth: 18
    implicitHeight: 18

    Rectangle {
        id: track
        anchors.fill: parent
        radius: width / 2
        color: "transparent"
        border.width: 2
        border.color: KikimoraTheme.borderSoft
        Accessible.ignored: true
    }
    Rectangle {
        id: dot
        width: 6
        height: 6
        radius: 3
        color: KikimoraTheme.accentPrimary
        visible: root.running
        anchors.horizontalCenter: parent.horizontalCenter
        y: 0
        Accessible.ignored: true

        RotationAnimator on rotation {
            target: dot
            from: 0
            to: 360
            duration: KikimoraTheme.animationNormal * 4
            loops: Animation.Infinite
            running: root.running && KikimoraTheme.motionEnabled
        }
        SequentialAnimation on y {
            loops: Animation.Infinite
            running: root.running && KikimoraTheme.motionEnabled
            NumberAnimation { to: dot.parent.height - dot.height; duration: 400; easing.type: Easing.InOutQuad }
            NumberAnimation { to: 0; duration: 400; easing.type: Easing.InOutQuad }
        }
    }
    Accessible.role: Accessible.Indicator
    Accessible.name: root.label.length > 0 ? root.label : (root.running ? "in progress" : "idle")
}

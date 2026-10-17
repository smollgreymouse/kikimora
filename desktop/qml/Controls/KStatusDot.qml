import QtQuick
import "../Theme"

Rectangle {
    id: root
    property string state: ""
    property int diameter: 9

    width: diameter
    height: diameter
    radius: diameter / 2
    color: {
        if (root.state === "Ready") return KikimoraTheme.success
        if (root.state === "Failed") return KikimoraTheme.error
        if (root.state === "Recovering" || root.state === "WaitingForUnderlay"
                || root.state === "Connecting" || root.state === "Disconnecting")
            return KikimoraTheme.warning
        return KikimoraTheme.textSecondary
    }

    Behavior on color { ColorAnimation { duration: KikimoraTheme.animationFast } }
}

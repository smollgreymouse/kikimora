pragma Singleton
import QtQuick

QtObject {
    readonly property color background: "#0E0E11"
    readonly property color surface: "#18181B"
    readonly property color surfaceHover: "#232327"
    readonly property color border: "#3F3F46"
    readonly property color textPrimary: "#FAFAFA"
    readonly property color textSecondary: "#A1A1AA"
    readonly property color accent: "#FBB26A"
    readonly property color success: "#4ADE80"
    readonly property color warning: "#EAB308"
    readonly property color error: "#EB5757"

    readonly property int radiusSmall: 12
    readonly property int radiusLarge: 20
    readonly property int spacingSmall: 8
    readonly property int spacing: 16
    readonly property int animationFast: 180
}

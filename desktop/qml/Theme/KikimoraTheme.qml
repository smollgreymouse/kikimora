pragma Singleton
import QtQuick

QtObject {
      readonly property SystemPalette systemPalette: SystemPalette {}

    readonly property color background: systemPalette.window
    readonly property color surface: systemPalette.base
    readonly property color surfaceHover: systemPalette.alternateBase
    readonly property color border: systemPalette.mid
    readonly property color textPrimary: systemPalette.windowText
    readonly property color textSecondary: systemPalette.text
      readonly property color accent: systemPalette.highlight
      readonly property color accentHover: systemPalette.highlight
      readonly property color accentText: systemPalette.highlightedText
    readonly property color success: "#477F52"
    readonly property color warning: "#EAB308"
    readonly property color error: "#EB5757"

    readonly property int radiusSmall: 10
    readonly property int radiusLarge: 16
    readonly property int spacingSmall: 8
    readonly property int spacing: 12
    readonly property int animationFast: 180
}

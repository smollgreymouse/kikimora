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

    // Token names required by the chapter-09 plan (identical values, stable
    // names so controls do not depend on palette roles directly).
    readonly property color backgroundBase: systemPalette.window
    readonly property color surfaceBase: systemPalette.base
    readonly property color surfacePressed: Qt.darker(systemPalette.alternateBase, 1.10)
    readonly property color textMuted: systemPalette.placeholderText
    readonly property color borderSoft: Qt.rgba(systemPalette.mid.r, systemPalette.mid.g, systemPalette.mid.b, 0.55)
    readonly property color accentPrimary: systemPalette.highlight
    readonly property color accentSuccess: success
    readonly property color accentWarning: warning
    readonly property color accentError: error

    // Reduced-motion switch owned by Settings (U13): animations are optional
    // decoration, never information.
    property bool motionEnabled: true

    readonly property int radiusSmall: 10
    readonly property int radiusLarge: 16
    readonly property int spacingSmall: 8
    readonly property int spacing: 12
    readonly property int animationFast: 180
    readonly property int animationNormal: 180
    readonly property int focusRingWidth: 2
}

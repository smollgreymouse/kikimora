pragma Singleton
import QtQuick

QtObject {
    readonly property SystemPalette systemPalette: SystemPalette {}
    readonly property bool darkMode: systemPalette.window.hslLightness < 0.5

    // Kikimora now defaults to a product-owned dark visual language inspired by
    // Amnezia: near-black canvas, graphite sheets and a warm amber interaction
    // colour. The light branch remains coherent for the explicit
    // KIKIMORA_THEME=light diagnostic/development override.
    readonly property color background: darkMode ? "#0E0E11" : systemPalette.window
    readonly property color backgroundRaised: darkMode ? "#151517" : systemPalette.window
    readonly property color surface: darkMode ? "#1C1D21" : systemPalette.base
    readonly property color surfaceRaised: darkMode ? "#24252A" : systemPalette.base
    readonly property color surfaceHover: darkMode ? "#2B2C31" : systemPalette.alternateBase
    readonly property color border: darkMode ? "#35363C" : systemPalette.mid
    readonly property color borderSoft: darkMode
                                        ? "#2B2C31"
                                        : Qt.rgba(systemPalette.mid.r,
                                                  systemPalette.mid.g,
                                                  systemPalette.mid.b, 0.55)

    readonly property color textPrimary: darkMode ? "#F4F1EF" : systemPalette.windowText
    readonly property color textSecondary: darkMode ? "#9A9BA2" : systemPalette.text
    readonly property color textMuted: darkMode ? "#70727A" : systemPalette.placeholderText

    // Warm Amnezia-like product accent. Semantic states keep their own colours
    // in rows, but the global connect control uses the product accent when ready.
    readonly property color accent: darkMode ? "#FBB26A" : systemPalette.highlight
    readonly property color accentHover: darkMode ? "#FFC184" : systemPalette.highlight
    readonly property color accentText: darkMode ? "#111114" : systemPalette.highlightedText
    readonly property color success: darkMode ? "#78C69A" : "#477F52"
    readonly property color warning: darkMode ? "#F2B85B" : "#EAB308"
    readonly property color error: darkMode ? "#FF6B72" : "#EB5757"

    // Stable token names used by controls and chapter-09 tests.
    readonly property color backgroundBase: background
    readonly property color surfaceBase: surface
    readonly property color surfacePressed: darkMode
                                             ? "#303137"
                                             : Qt.darker(systemPalette.alternateBase, 1.10)
    readonly property color accentPrimary: accent
    readonly property color accentSuccess: success
    readonly property color accentWarning: warning
    readonly property color accentError: error

    readonly property color glowAccentSoft: darkMode
                                            ? Qt.rgba(251 / 255, 178 / 255, 106 / 255, 0.12)
                                            : Qt.rgba(accent.r, accent.g, accent.b, 0.08)
    readonly property color glowAccentStrong: darkMode
                                              ? Qt.rgba(251 / 255, 178 / 255, 106 / 255, 0.24)
                                              : Qt.rgba(accent.r, accent.g, accent.b, 0.16)

    // Reduced-motion switch owned by Settings: animations are decoration only.
    property bool motionEnabled: true

    readonly property int radiusSmall: 12
    readonly property int radiusLarge: 22
    readonly property int spacingSmall: 8
    readonly property int spacing: 12
    readonly property int animationFast: 180
    readonly property int animationNormal: 220
    readonly property int focusRingWidth: 2
}

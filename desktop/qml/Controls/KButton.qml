import QtQuick
import QtQuick.Controls
import "../Theme"

Button {
    id: root
    property bool primary: false
    property bool danger: false

    implicitHeight: 40
    hoverEnabled: true

    background: Rectangle {
        radius: KikimoraTheme.radiusSmall
        color: {
            if (root.danger) return root.hovered ? Qt.lighter(KikimoraTheme.error, 1.08) : KikimoraTheme.error
            if (root.primary) return root.hovered ? KikimoraTheme.accentHover : KikimoraTheme.accent
            return root.hovered ? KikimoraTheme.surfaceHover : KikimoraTheme.surface
        }
        border.width: (root.primary || root.danger) ? 0 : 1
        border.color: KikimoraTheme.border
        Behavior on color { ColorAnimation { duration: KikimoraTheme.animationFast } }
    }

    contentItem: Text {
        text: root.text
        color: root.danger ? "#FFFFFF"
                           : (root.primary ? KikimoraTheme.accentText : KikimoraTheme.textPrimary)
        font.pixelSize: 14
        font.weight: (root.primary || root.danger) ? Font.DemiBold : Font.Normal
        horizontalAlignment: Text.AlignHCenter
        verticalAlignment: Text.AlignVCenter
        elide: Text.ElideRight
    }
}

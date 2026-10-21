import QtQuick
import QtQuick.Controls
import "../Theme"

// Compact square action with an icon source and an explicit accessible name:
// icon-only buttons are not allowed to be unlabelled.
Button {
    id: root
    property string accessibleText: ""
    property url iconSource
    property string glyph: ""
    implicitWidth: 34
    implicitHeight: 34
    padding: 0
    Accessible.role: Accessible.Button
    Accessible.name: accessibleText.length > 0 ? accessibleText : text

    background: Rectangle {
        radius: KikimoraTheme.radiusSmall
        color: root.down ? KikimoraTheme.surfacePressed
                         : (root.hovered ? KikimoraTheme.surfaceHover : KikimoraTheme.surfaceBase)
        border.width: root.activeFocus ? KikimoraTheme.focusRingWidth : 1
        border.color: root.activeFocus ? KikimoraTheme.accentPrimary : KikimoraTheme.borderSoft
    }
    contentItem: Item {
        Image {
            anchors.centerIn: parent
            width: 20
            height: 20
            visible: root.iconSource.toString().length > 0
            source: root.iconSource
            sourceSize: Qt.size(20, 20)
            smooth: true
        }
        Text {
            anchors.centerIn: parent
            visible: root.iconSource.toString().length === 0
            text: root.glyph.length > 0 ? root.glyph : root.text
            color: KikimoraTheme.textPrimary
            font.pixelSize: 14
        }
    }
}

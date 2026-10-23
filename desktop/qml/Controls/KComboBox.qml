import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "../Theme"

// Labelled combo box used for profile selection. The inner control keeps its
// own objectName so tests and QML can address it directly.
ColumnLayout {
    id: root
    property string label: ""
    property alias model: combo.model
    property alias currentIndex: combo.currentIndex
    readonly property alias count: combo.count
    property alias textRole: combo.textRole
    property string comboObjectName: ""
    signal activated(int index)

    spacing: 4
    Layout.fillWidth: true

    Text {
        visible: root.label.length > 0
        text: root.label
        color: KikimoraTheme.textSecondary
        font.pixelSize: 11
    }
    ComboBox {
        id: combo
        objectName: root.comboObjectName
        Layout.fillWidth: true
        Accessible.name: root.label
        onActivated: function(index) { root.activated(index) }
        indicator: Text {
            x: combo.width - width - 14
            anchors.verticalCenter: parent.verticalCenter
            text: "⌄"
            color: KikimoraTheme.textSecondary
            font.pixelSize: 19
        }
        contentItem: Text {
            leftPadding: 12
            rightPadding: 32
            text: combo.displayText
            color: KikimoraTheme.textPrimary
            font.pixelSize: 14
            verticalAlignment: Text.AlignVCenter
            elide: Text.ElideRight
        }
        background: Rectangle {
            radius: KikimoraTheme.radiusSmall
            color: KikimoraTheme.surfaceRaised
            border.width: combo.activeFocus ? 2 : 1
            border.color: combo.activeFocus ? KikimoraTheme.accent : KikimoraTheme.border
        }
        delegate: ItemDelegate {
            width: combo.width
            text: String(modelData)
            contentItem: Text {
                text: parent.text
                color: KikimoraTheme.textPrimary
                verticalAlignment: Text.AlignVCenter
                leftPadding: 12
            }
            background: Rectangle {
                color: highlighted ? KikimoraTheme.surfaceHover : KikimoraTheme.surface
            }
        }
        popup: Popup {
            y: combo.height + 3
            width: combo.width
            padding: 4
            implicitHeight: Math.min(320, contentItem.implicitHeight + 8)
            contentItem: ListView {
                clip: true
                implicitHeight: contentHeight
                model: combo.popup.visible ? combo.delegateModel : null
                currentIndex: combo.highlightedIndex
            }
            background: Rectangle {
                radius: KikimoraTheme.radiusSmall
                color: KikimoraTheme.surface
                border.color: KikimoraTheme.border
            }
        }
    }
}

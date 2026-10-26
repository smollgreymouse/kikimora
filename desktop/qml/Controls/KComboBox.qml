import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "../Theme"

// Labelled combo box used for profile selection. The inner control keeps its
// own objectName so tests and QML can address it directly.
ColumnLayout {
    id: root
    property string label: ""
    property bool titleMode: false
    // Accessible name for combos labelled by a neighbouring row instead of their
    // own label text.
    property string accessibleName: ""
    // Shown instead of the current item when no item is selected: the confirmed
    // value may live outside this model (for example a role of another profile).
    property string placeholderText: ""
    property alias model: combo.model
    property alias currentIndex: combo.currentIndex
    readonly property alias count: combo.count
    property alias textRole: combo.textRole
    property string comboObjectName: ""
    signal activated(int index)

    spacing: 4
    Layout.fillWidth: true

    Text {
        visible: root.label.length > 0 && !root.titleMode
        text: root.label
        color: KikimoraTheme.textSecondary
        font.pixelSize: 11
    }
    ComboBox {
        id: combo
        objectName: root.comboObjectName
        Layout.fillWidth: true
        // Fixed control heights match the KButton metric (40) so combo+button
        // rows stay on one line; titleMode uses the taller title row (44).
        Layout.preferredHeight: root.titleMode ? 44 : 40
        Accessible.name: root.label.length > 0 ? root.label : root.accessibleName
        onActivated: function(index) { root.activated(index) }
        indicator: Text {
            x: combo.width - width - 14
            anchors.verticalCenter: parent.verticalCenter
            text: "⌄"
            color: KikimoraTheme.textSecondary
            font.pixelSize: root.titleMode ? 22 : 19
        }
        contentItem: Text {
            leftPadding: root.titleMode ? 2 : 12
            rightPadding: root.titleMode ? 30 : 32
            text: combo.currentIndex < 0 && root.placeholderText.length > 0
                  ? root.placeholderText
                  : combo.displayText
            color: combo.currentIndex < 0 && root.placeholderText.length > 0
                   ? KikimoraTheme.textSecondary
                   : KikimoraTheme.textPrimary
            font.pixelSize: root.titleMode ? 21 : 14
            font.weight: root.titleMode ? Font.DemiBold : Font.Normal
            verticalAlignment: Text.AlignVCenter
            elide: Text.ElideRight
        }
        background: Rectangle {
            radius: KikimoraTheme.radiusSmall
            color: root.titleMode ? (combo.hovered || combo.activeFocus ? KikimoraTheme.surfaceHover : "transparent") : KikimoraTheme.surfaceRaised
            border.width: root.titleMode ? 0 : (combo.activeFocus ? 2 : 1)
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

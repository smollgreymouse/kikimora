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
    }
}

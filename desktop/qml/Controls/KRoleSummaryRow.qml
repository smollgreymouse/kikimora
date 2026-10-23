import QtQuick
import QtQuick.Layouts
import "../Theme"

// Compact collapsed role row (plan U5): one click opens the role detail drawer.
// RoleRow keeps the visual and the "roleRow" objectName the behaviour tests use;
// this component adds the bounded activity indicator for busy states.
Item {
    id: root
    property string label: ""
    property string protocol: ""
    property string server: ""
    property string state: ""
    property string stateText: ""
    property bool selected: false
    property string rowName: "roleRow"
    signal openRequested()

    implicitHeight: row.implicitHeight
    Layout.fillWidth: true

    RoleRow {
        id: row
        anchors.fill: parent
        label: root.label
        protocol: root.protocol
        server: root.server
        state: root.state
        stateText: root.stateText
        selected: root.selected
        rowName: root.rowName
        onOpenRequested: root.openRequested()
    }


    // The press target is explicit: a button inside a scrolling list does not always see the
    // synthetic press, and the row looked dead in the sheet and in the accordion.
    MouseArea {
        anchors.fill: parent
        z: 5
        preventStealing: true
        onClicked: root.openRequested()
    }
}

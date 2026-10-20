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
        onOpenRequested: root.openRequested()
    }

}

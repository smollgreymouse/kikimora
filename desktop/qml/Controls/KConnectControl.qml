import QtQuick
import "../Theme"

// One aggregate connect/disconnect control. The larger stage deliberately keeps
// the ring isolated on the dark canvas, matching the visual hierarchy of the
// Amnezia desktop client.
Item {
    id: root
    property string stateText: ""
    property string actionText: ""
    signal activated()

    implicitHeight: 244
    Accessible.role: Accessible.Button
    Accessible.name: actionText.length > 0 ? actionText : stateText
    Accessible.description: stateText

    Item {
        objectName: "connectCircleHost"
        anchors.fill: parent

        ConnectCircle {
            objectName: "connectCircle"
            anchors.centerIn: parent
            state: Core.aggregateState
            actionText: root.actionText
            stateText: root.stateText
            onClicked: root.activated()
        }
    }
}

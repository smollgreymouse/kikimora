import QtQuick
import "../Theme"

// The single aggregate connect/disconnect control. It owns exactly one
// activation and hands it to the core command bridge; QML never loops roles
// here and never infers the command from role states.
Item {
    id: root
    property string stateText: ""
    property string actionText: ""
    signal activated()

    implicitHeight: 210
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

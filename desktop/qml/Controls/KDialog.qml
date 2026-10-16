import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "../Theme"

// Simple modal dialog with one body component and standard actions.
Dialog {
    id: root
    property string bodyText: ""
    property string acceptText: "Close"
    property string rejectText: ""
    default property alias extraContent: extraHolder.data

    modal: true
    width: parent ? Math.min(parent.width - 40, 420) : 380
    x: parent ? (parent.width - width) / 2 : 0
    y: parent ? (parent.height - height) / 2 : 0
    padding: 16
    // Dialog is a popup, not an Item: the accessible name lives on the
    // dialog header text instead of an Accessible attached property here.

    contentItem: ColumnLayout {
        spacing: 10
        KHeader { title: root.title; subtitle: root.bodyText }
        ColumnLayout {
            id: extraHolder
            Layout.fillWidth: true
            spacing: 8
        }
    }
    standardButtons: root.rejectText.length > 0 ? (Dialog.Ok | Dialog.Cancel) : Dialog.Ok
    onAccepted: root.close()
    Component.onCompleted: {
        if (standardButtons & Dialog.Ok) {
            const okButton = standardButton(Dialog.Ok)
            if (okButton) okButton.text = root.acceptText
        }
    }
}

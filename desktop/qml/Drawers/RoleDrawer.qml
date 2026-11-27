import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "../Theme"
import "../Controls"

// Role detail drawer (plan U5). The full field surface lives in
// KRoleDetailDrawer; this file keeps the drawer contract used by Main.qml and
// the behaviour tests ("roleDrawer", drawerRoleTitle, roleActionButton).
KDrawer {
    id: root
    objectName: "roleDrawer"

    // 09G-W2.2: role identity is the stable core id, never a model row.
    property string roleId: ""
    property var roleData: ({})
    signal roleActionRequested(string roleId, string action)

    onRoleDataChanged: {
        roleDetails.sectionIndex = 0
        roleDetails.diagnosticsExpanded = false
    }

    closePolicy: roleDetails.routeDirty
                 ? Popup.NoAutoClose
                 : (Popup.CloseOnEscape | Popup.CloseOnPressOutside)

    RowLayout {
        Layout.fillWidth: true
        KButton {
            objectName: "closeRoleDrawerButton"
            text: "Закрыть"
            onClicked: {
                if (roleDetails.routeDirty)
                    discardRoutesDialog.open()
                else
                    root.close()
            }
        }
        Text {
            Layout.fillWidth: true
            horizontalAlignment: Text.AlignRight
            color: KikimoraTheme.warning
            font.pixelSize: 11
            text: roleDetails.routeDirty ? "Есть несохранённые маршруты" : ""
        }
    }

    Dialog {
        id: discardRoutesDialog
        objectName: "discardRoutesDialog"
        parent: Overlay.overlay
        anchors.centerIn: parent
        modal: true
        width: Math.min(320, root.width - 32)
        title: "Несохранённые маршруты"
        contentItem: ColumnLayout {
            spacing: 12
            Text {
                Layout.fillWidth: true
                text: "Изменения в списке маршрутов будут потеряны. Закрыть роль?"
                color: KikimoraTheme.textPrimary
                wrapMode: Text.WordWrap
            }
            RowLayout {
                Layout.fillWidth: true
                KButton {
                    objectName: "keepRouteDraftButton"
                    Layout.fillWidth: true
                    text: "Отмена"
                    onClicked: discardRoutesDialog.reject()
                }
                KButton {
                    objectName: "discardRouteDraftButton"
                    Layout.fillWidth: true
                    text: "Не сохранять"
                    onClicked: {
                        roleDetails.discardRoutes()
                        discardRoutesDialog.close()
                        root.close()
                    }
                }
            }
        }
        background: Rectangle {
            radius: KikimoraTheme.radiusSmall
            color: KikimoraTheme.surface
        }
    }

    KRoleDetailDrawer {
        id: roleDetails
        roleId: root.roleId
        roleData: root.roleData
        onRoleActionRequested: function(roleId, action) {
            root.roleActionRequested(roleId, action)
            root.close()
        }
    }
}

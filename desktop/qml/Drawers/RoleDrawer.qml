import QtQuick
import "../Controls"

// Role detail drawer (plan U5). The full field surface lives in
// KRoleDetailDrawer; this file keeps the drawer contract used by Main.qml and
// the behaviour tests ("roleDrawer", drawerRoleTitle, roleActionButton).
KDrawer {
    id: root
    objectName: "roleDrawer"

    property int roleIndex: -1
    property var roleData: ({})
    signal roleActionRequested(int roleIndex, string action)

    KRoleDetailDrawer {
        roleIndex: root.roleIndex
        roleData: root.roleData
        onRoleActionRequested: function(index, action) {
            root.roleActionRequested(index, action)
            root.close()
        }
    }
}

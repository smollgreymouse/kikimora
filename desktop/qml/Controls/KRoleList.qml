import QtQuick
import QtQuick.Layouts
import "../Theme"

// One role list for the whole UI: the main page, the profile sheet and the routing page all show
// the same rows and differ only by which profile filters them. A click always means "open the
// role", the caller decides what the panel does with it.
Item {
    id: root

    // Profile whose roles are listed. Empty means every role.
    property string profile: ""
    // Role that is currently open in a panel, selected in the list.
    property string currentRole: ""
    // Optional height limit; the list scrolls inside it.
    property int maximumHeight: 0
    // Name of the row objects: pages that render more than one such list keep them apart.
    property string rowName: "roleRow"

    signal roleActivated(string label)

    implicitHeight: list.contentHeight

    function rolesForProfile() {
        const out = []
        for (let i = 0; i < Core.roleLabels.length; ++i) {
            const label = String(Core.roleLabels[i])
            if (root.profile.length > 0) {
                const membership = Core.profilesForRole(label) || []
                // A role without a recorded membership is shown everywhere: a fresh role must not
                // disappear from the page that created it.
                if (membership.length > 0 && membership.indexOf(root.profile) < 0)
                    continue
            }
            const role = Core.roles.get(i)
            if (role && role.label !== undefined)
                out.push(role)
        }
        return out
    }

    ListView {
        id: list
        anchors.fill: parent

        spacing: 8
        clip: true
        model: root.rolesForProfile()

        delegate: Item {
            id: rowItem
            required property var modelData

            width: ListView.view.width
            height: row.implicitHeight

            KRoleSummaryRow {
                id: row
                anchors.fill: parent
                rowName: root.rowName
                label: String(modelData.label || "")
                protocol: String(modelData.protocol || "")
                server: String(modelData.server || "")
                state: String(modelData.state || "")
                stateText: String(modelData.stateText || "")
                selected: root.currentRole.length > 0 && root.currentRole === label
                onOpenRequested: root.roleActivated(label)
            }


        }
    }
}

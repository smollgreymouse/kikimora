import QtQuick
import QtQuick.Layouts
import "../Theme"

// Last row of a profile's role list: where the traffic no role claims goes.
// The core keeps one global rule, but the choice is always made from the roles
// that are actually present in the profile the user is looking at. A rule that
// points at a role from another profile is shown by name instead of being
// silently replaced with the first available option.
Item {
    id: root
    objectName: "defaultRouteRow"

    // Labels of the roles that belong to this profile, in list order.
    property var roleLabels: []
    // The confirmed rule: "direct" or the name of a role.
    property string mode: "direct"
    property string comboObjectName: "defaultRouteCombo"

    readonly property string directLabel: "Напрямую"
    readonly property var options: [directLabel].concat(roleLabels)

    signal modeSelected(string mode)

    function indexForMode() {
        return options.indexOf(mode)
    }

    implicitHeight: 64

    Rectangle {
        anchors.fill: parent
        radius: KikimoraTheme.radiusSmall
        color: KikimoraTheme.surfaceRaised
        border.width: 1
        border.color: KikimoraTheme.borderSoft

        RowLayout {
            anchors.fill: parent
            anchors.leftMargin: 14
            anchors.rightMargin: 12
            spacing: 10

            ColumnLayout {
                Layout.fillWidth: true
                spacing: 2

                Text {
                    Layout.fillWidth: true
                    text: "Маршрут по умолчанию"
                    color: KikimoraTheme.textPrimary
                    font.pixelSize: 13
                    font.weight: Font.DemiBold
                    elide: Text.ElideRight
                }

                Text {
                    Layout.fillWidth: true
                    text: "Трафик вне ролей"
                    color: KikimoraTheme.textSecondary
                    font.pixelSize: 11
                    elide: Text.ElideRight
                }
            }

            KComboBox {
                id: combo
                objectName: root.comboObjectName
                accessibleName: "Маршрут по умолчанию"
                Layout.preferredWidth: 132
                model: root.options
                placeholderText: root.mode !== "direct" ? root.mode + " · другой профиль" : ""
                onActivated: function(index) {
                    const chosen = String(root.options[index])
                    root.modeSelected(chosen === root.directLabel ? "direct" : chosen)
                    // The core confirms through `mode`; until it does, the row keeps
                    // showing the last confirmed rule instead of an unconfirmed choice.
                    combo.currentIndex = root.indexForMode()
                }
            }
        }
    }

    // The combo only ever shows the confirmed rule: a click snaps back until the
    // core replies, a model rebuild re-resolves the index, and a rule that points
    // at another profile's role stays a name instead of becoming the first option.
    function enforceIndex() {
        combo.currentIndex = root.indexForMode()
    }

    onModeChanged: enforceIndex()
    onRoleLabelsChanged: Qt.callLater(enforceIndex)
}

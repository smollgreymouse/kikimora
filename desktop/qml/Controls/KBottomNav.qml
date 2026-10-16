import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "../Theme"

// Bottom navigation. Tabs are buttons with an icon and a visible label tooltip;
// each tab reports an accessible name and its selected state, so navigation is
// usable without a pointer.
RowLayout {
    id: root
    property var items: []
    property int selectedTab: 0
    signal tabSelected(int index)

    Layout.fillWidth: true
    Layout.minimumHeight: 48
    Layout.preferredHeight: 48
    Layout.maximumHeight: 48
    spacing: 6

    Repeater {
        model: root.items
        delegate: Button {
            id: tabButton
            objectName: "bottomTabButton"
            required property int index
            required property var modelData
            Layout.fillWidth: true
            Layout.minimumHeight: 48
            Layout.preferredHeight: 48
            Layout.maximumHeight: 48
            text: modelData.label
            activeFocusOnTab: true
            Accessible.role: Accessible.PageTab
            Accessible.name: tabButton.text
            Accessible.selected: root.selectedTab === tabButton.index
            onClicked: root.tabSelected(tabButton.index)
            ToolTip.visible: hovered
            ToolTip.text: tabButton.text
            ToolTip.delay: 500
            background: Rectangle {
                radius: 14
                color: root.selectedTab === tabButton.index ? KikimoraTheme.surfaceHover : "transparent"
                border.width: tabButton.activeFocus ? KikimoraTheme.focusRingWidth : 0
                border.color: KikimoraTheme.accentPrimary
            }
            contentItem: Item {
                Image {
                    objectName: "tabIcon"
                    anchors.centerIn: parent
                    width: 24
                    height: 24
                    source: "image://system/" + tabButton.modelData.iconName
                    sourceSize: Qt.size(24, 24)
                    smooth: true
                }
            }
        }
    }
}

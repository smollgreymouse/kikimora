import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "../Theme"

Rectangle {
    id: root
    property var items: []
    property int selectedTab: 0
    signal tabSelected(int index)

    Layout.fillWidth: true
    Layout.minimumHeight: 62
    Layout.preferredHeight: 62
    Layout.maximumHeight: 62
    color: KikimoraTheme.surface

    Rectangle {
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.top: parent.top
        height: 1
        color: KikimoraTheme.borderSoft
    }

    RowLayout {
        anchors.fill: parent
        anchors.leftMargin: 12
        anchors.rightMargin: 12
        anchors.topMargin: 7
        anchors.bottomMargin: 7
        spacing: 6

        Repeater {
            model: root.items

            delegate: Button {
                id: tabButton
                objectName: "bottomTabButton"
                required property int index
                required property var modelData

                Layout.fillWidth: true
                Layout.fillHeight: true
                text: modelData.label
                hoverEnabled: true
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
                    color: root.selectedTab === tabButton.index
                           ? KikimoraTheme.backgroundRaised
                           : (tabButton.hovered ? KikimoraTheme.surfaceHover : "transparent")
                    border.width: tabButton.activeFocus ? KikimoraTheme.focusRingWidth : 0
                    border.color: KikimoraTheme.accentPrimary
                    Behavior on color { ColorAnimation { duration: KikimoraTheme.animationFast } }
                }

                contentItem: Item {
                    Image {
                        objectName: "tabIcon"
                        anchors.centerIn: parent
                        width: 24
                        height: 24
                        source: "image://system/"
                                + (root.selectedTab === tabButton.index ? "accent/" : (KikimoraTheme.darkMode ? "muted/" : "light/"))
                                + tabButton.modelData.iconName
                        sourceSize: Qt.size(24, 24)
                        smooth: true
                    }
                }
            }
        }
    }
}

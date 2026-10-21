import QtQuick
import QtQuick.Layouts
import "../Theme"

// A list of bars with a single plus tile underneath - the shape the home page uses for roles.
// The profiles page reuses it so both lists look and behave the same: click a bar to act on it,
// click the plus to add something.
ColumnLayout {
    id: root

    property var model: null
    property string activeValue: ""
    property string tagText: "АКТИВНЫЙ"
    property string plusObjectName: "barsPlusButton"
    property string plusHint: "Добавить"
    property bool plusVisible: true

    signal rowClicked(int index, string value)
    signal plusClicked()

    spacing: 8

    Repeater {
        model: root.model

        delegate: Rectangle {
            required property int index
            required property string modelData

            Layout.fillWidth: true
            Layout.preferredHeight: 52
            radius: KikimoraTheme.radiusSmall
            color: root.activeValue === modelData ? KikimoraTheme.surfaceHover : KikimoraTheme.surface
            border.width: 1
            border.color: root.activeValue === modelData ? KikimoraTheme.accentPrimary : KikimoraTheme.borderSoft

            RowLayout {
                anchors.fill: parent
                anchors.leftMargin: 12
                anchors.rightMargin: 12
                spacing: 8

                Text {
                    Layout.fillWidth: true
                    text: modelData
                    color: KikimoraTheme.textPrimary
                    font.pixelSize: 14
                    font.weight: Font.DemiBold
                    elide: Text.ElideRight
                }

                KTag {
                    visible: root.activeValue === modelData
                    text: root.tagText
                    tagColor: KikimoraTheme.accentPrimary
                }

                Text {
                    text: "\u203a"
                    color: KikimoraTheme.textSecondary
                    font.pixelSize: 18
                }
            }

            MouseArea {
                anchors.fill: parent
                onClicked: root.rowClicked(index, modelData)
            }
        }
    }

    Rectangle {
        objectName: root.plusObjectName
        visible: root.plusVisible
        Layout.fillWidth: true
        Layout.preferredHeight: 52
        radius: KikimoraTheme.radiusSmall
        color: KikimoraTheme.surface
        Accessible.role: Accessible.Button
        Accessible.name: root.plusHint

        Text {
            anchors.centerIn: parent
            text: "+"
            color: KikimoraTheme.textSecondary
            font.pixelSize: 26
        }

        MouseArea {
            anchors.fill: parent
            onClicked: root.plusClicked()
        }
    }
}

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts

Item {
    ColumnLayout {
        anchors.fill: parent
        spacing: 14

        Text {
            text: "Power"
            color: "#2c302a"
            font.pixelSize: 22
            font.weight: Font.DemiBold
        }
        Text {
            text: "This box stays up with the lid closed. Sleep and suspend keys are ignored so mesh services keep running."
            color: "#6e7568"
            font.pixelSize: 12
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        Rectangle {
            Layout.fillWidth: true
            radius: 12
            color: "#f7f9f4"
            border.color: "#b9c9ab"
            border.width: 1
            implicitHeight: col.height + 28

            Column {
                id: col
                anchors.left: parent.left
                anchors.right: parent.right
                anchors.top: parent.top
                anchors.margins: 14
                spacing: 8

                Text {
                    text: "POLICY"
                    color: "#5c6b52"
                    font.pixelSize: 10
                    font.letterSpacing: 2
                }
                Text {
                    width: parent.width
                    text: "HandleLidSwitch=ignore\nHandleSuspendKey=ignore\nIdleAction=ignore"
                    color: "#2c302a"
                    font.family: "Noto Sans Mono"
                    font.pixelSize: 12
                    wrapMode: Text.Wrap
                }
            }
        }

        Text {
            text: "Use દાદી menu → Sleep Display to blank the panel without suspending compute."
            color: "#a8af9f"
            font.pixelSize: 11
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        Item { Layout.fillHeight: true }
    }
}

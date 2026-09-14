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
            font.pixelSize: 20
            font.weight: Font.DemiBold
        }
        Text {
            text: "The session is dadi with no lock screen. Lid close, idle, and suspend keys do not sleep the box. The panel stays on."
            color: "#6e7568"
            font.pixelSize: 13
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
                    text: "Autologin dadi · Relogin=true\nkscreenlocker Autolock=false\nHandleLidSwitch=ignore\nsystemd-inhibit idle:sleep:lid"
                    color: "#2c302a"
                    font.pixelSize: 13
                    wrapMode: Text.Wrap
                }
            }
        }

        Item { Layout.fillHeight: true }
    }
}

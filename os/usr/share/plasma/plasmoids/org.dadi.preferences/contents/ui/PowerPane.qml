import QtQuick
import QtQuick.Layouts

Item {
    ColumnLayout {
        anchors.fill: parent
        spacing: 14

        Text {
            text: "Power"
            color: "#141511"
            font.pixelSize: 22
            font.weight: Font.DemiBold
            font.letterSpacing: -0.3
        }
        Text {
            text: "The session is dadi with no lock screen. Lid close, idle, and suspend keys do not sleep the box. The panel stays on."
            color: "#8a8e87"
            font.pixelSize: 13
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }
        Text {
            text: "Autologin is dadi with no password. SSH users live under Users. The locker is off; PowerDevil lid and idle do nothing."
            color: "#141511"
            font.pixelSize: 13
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        Item { Layout.fillHeight: true }
    }
}

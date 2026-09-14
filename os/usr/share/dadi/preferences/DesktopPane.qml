import QtQuick
import QtQuick.Controls
import QtQuick.Layouts

Item {
    id: root

    property var runner
    signal toast(string message)

    ColumnLayout {
        anchors.fill: parent
        spacing: 14

        Text {
            text: "Desktop"
            color: "#2c302a"
            font.pixelSize: 20
            font.weight: Font.DemiBold
        }
        Text {
            text: "Field wallpaper, bone glass widgets (agents, memory, timeline, system), translucent top bar, floating dock. Icons stay off."
            color: "#6e7568"
            font.pixelSize: 13
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        Button {
            Layout.preferredHeight: 40
            Layout.preferredWidth: 180
            onClicked: {
                if (root.runner)
                    root.runner.exec("/usr/libexec/dadi/apply-desktop.sh --force")
                root.toast("Desktop layout reset")
            }
            background: Rectangle {
                radius: 9
                color: parent.down ? "#5c6b52" : "#8fa382"
            }
            contentItem: Text {
                text: "Reset layout"
                color: "#fafaf7"
                horizontalAlignment: Text.AlignHCenter
                verticalAlignment: Text.AlignVCenter
                font.pixelSize: 13
            }
        }

        Item { Layout.fillHeight: true }
    }
}

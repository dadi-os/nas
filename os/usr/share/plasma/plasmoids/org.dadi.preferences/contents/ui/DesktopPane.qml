import QtQuick
import QtQuick.Controls
import QtQuick.Layouts

Item {
    id: root

    property var runner
    signal toast(string message)

    ColumnLayout {
        anchors.fill: parent
        spacing: 12

        Text {
            text: "Desktop"
            color: "#2c302a"
            font.pixelSize: 22
            font.weight: Font.DemiBold
        }
        Text {
            text: "Wallpaper field for bone glass. Bloom is default — soft sage orb so veil panels catch light."
            color: "#6e7568"
            font.pixelSize: 12
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        Repeater {
            model: ["Bloom", "Mist", "Vein"]
            Button {
                required property string modelData
                Layout.fillWidth: true
                text: "Use " + modelData
                onClicked: {
                    if (root.runner && root.runner.exec)
                        root.runner.exec("dadi-wallpaper " + modelData)
                    root.toast(modelData + " wallpaper applied")
                }
                background: Rectangle {
                    radius: 9
                    color: "#f7f9f4"
                    border.color: "#b9c9ab"
                    border.width: 1
                }
                contentItem: Text {
                    text: parent.text
                    color: "#5c6b52"
                    horizontalAlignment: Text.AlignHCenter
                    verticalAlignment: Text.AlignVCenter
                }
            }
        }

        Text {
            text: "Panel blur, dock autohide, and icons-off ship with org.dadi.desktop. Reset: lookandfeeltool -a org.dadi.desktop --resetLayout"
            color: "#a8af9f"
            font.pixelSize: 11
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        Item { Layout.fillHeight: true }
    }
}

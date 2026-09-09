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
            text: "Field is the sage leaf and glass leaf wallpaper under bone glass. Panel blur, dock autohide, and icons-off ship with org.dadi.desktop."
            color: "#6e7568"
            font.pixelSize: 12
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        Text {
            text: "Reset layout: lookandfeeltool -a org.dadi.desktop --resetLayout"
            color: "#a8af9f"
            font.pixelSize: 11
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        Item { Layout.fillHeight: true }
    }
}

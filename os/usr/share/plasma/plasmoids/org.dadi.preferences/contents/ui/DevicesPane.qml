import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.dadi.Desktop

Item {
    id: root

    property var runner

    ColumnLayout {
        anchors.fill: parent
        spacing: 14

        Text {
            text: "Devices"
            color: "#141511"
            font.pixelSize: 22
            font.weight: Font.DemiBold
            font.letterSpacing: -0.3
        }
        Text {
            text: "Provision a new Hath. Create a setup QR on this box, then scan it from the phone or laptop."
            color: "#8a8e87"
            font.pixelSize: 13
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        DadiButton {
            text: "Open add device"
            Layout.alignment: Qt.AlignLeft
            onClicked: {
                if (root.runner)
                    root.runner.exec("dadi-add-device")
            }
        }

        Item { Layout.fillHeight: true }
    }
}

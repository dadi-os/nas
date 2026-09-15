import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.dadi.Desktop

Item {
    id: root

    property var runner
    signal toast(string message)

    ColumnLayout {
        anchors.fill: parent
        spacing: 14

        Text {
            text: "Desktop"
            color: "#141511"
            font.pixelSize: 22
            font.weight: Font.DemiBold
            font.letterSpacing: -0.3
        }
        Text {
            text: "Glass widgets, translucent top bar, floating dock. Icons stay off."
            color: "#8a8e87"
            font.pixelSize: 13
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        DadiButton {
            text: "Reset layout"
            Layout.alignment: Qt.AlignLeft
            onClicked: {
                if (root.runner)
                    root.runner.exec("/usr/libexec/dadi/apply-desktop.sh --force")
                root.toast("Desktop layout reset")
            }
        }

        Item { Layout.fillHeight: true }
    }
}

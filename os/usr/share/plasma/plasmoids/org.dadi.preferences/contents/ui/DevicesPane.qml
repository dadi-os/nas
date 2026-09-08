import QtQuick
import QtQuick.Controls
import QtQuick.Layouts

Item {
    id: root

    property var runner

    ColumnLayout {
        anchors.fill: parent
        spacing: 16

        Text {
            text: "Devices"
            color: "#2c302a"
            font.pixelSize: 22
            font.weight: Font.DemiBold
        }
        Text {
            text: "Provision a new Hath. Create a setup QR on this box, then scan it from the phone or laptop install."
            color: "#6e7568"
            font.pixelSize: 13
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        Rectangle {
            Layout.fillWidth: true
            Layout.preferredHeight: card.implicitHeight + 32
            radius: 12
            color: "#f7f9f4"
            border.color: "#b9c9ab"
            border.width: 1

            ColumnLayout {
                id: card
                anchors.left: parent.left
                anchors.right: parent.right
                anchors.top: parent.top
                anchors.margins: 16
                spacing: 10

                Text {
                    text: "ADD DEVICE"
                    color: "#a8af9f"
                    font.pixelSize: 10
                    font.letterSpacing: 2.5
                }
                Text {
                    text: "Sage QR with the દાદી mark. Single-use · about one hour."
                    color: "#6e7568"
                    font.pixelSize: 12
                    wrapMode: Text.WordWrap
                    Layout.fillWidth: true
                }
                Button {
                    Layout.topMargin: 4
                    Layout.preferredHeight: 40
                    onClicked: {
                        if (root.runner)
                            root.runner.exec("dadi-add-device")
                    }
                    background: Rectangle {
                        radius: 12
                        color: parent.down ? "#5c6b52" : "#8fa382"
                    }
                    contentItem: Text {
                        text: "OPEN ADD DEVICE"
                        color: "#fafaf7"
                        font.pixelSize: 12
                        font.letterSpacing: 1.5
                        horizontalAlignment: Text.AlignHCenter
                        verticalAlignment: Text.AlignVCenter
                    }
                }
            }
        }

        Item { Layout.fillHeight: true }
    }
}

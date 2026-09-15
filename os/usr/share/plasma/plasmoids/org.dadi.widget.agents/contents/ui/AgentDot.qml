import QtQuick

Item {
    id: root
    property string visual: "idle"
    property bool isRoot: false
    property string label: ""
    property string caption: "idle"

    readonly property real disc: isRoot ? 22 : 16
    readonly property real anchorY: disc / 2 + 2

    width: 132
    height: 76
    scale: hover.hovered ? 1.05 : 1

    Behavior on scale {
        NumberAnimation { duration: 160; easing.type: Easing.OutCubic }
    }

    HoverHandler { id: hover }

    Column {
        anchors.horizontalCenter: parent.horizontalCenter
        spacing: 7
        width: parent.width

        Item {
            anchors.horizontalCenter: parent.horizontalCenter
            width: root.disc + 10
            height: root.disc + 4

            Rectangle {
                visible: root.visual === "running"
                anchors.centerIn: parent
                width: root.disc + 10
                height: width
                radius: width / 2
                color: "#14151118"
                SequentialAnimation on scale {
                    running: root.visual === "running"
                    loops: Animation.Infinite
                    NumberAnimation { from: 0.9; to: 1.12; duration: 1200; easing.type: Easing.InOutSine }
                    NumberAnimation { from: 1.12; to: 0.9; duration: 1200; easing.type: Easing.InOutSine }
                }
            }

            Rectangle {
                anchors.centerIn: parent
                width: root.disc
                height: width
                radius: width / 2
                color: root.visual === "dormant" ? "transparent" : "#141511"
                border.width: root.visual === "dormant" ? 1.5 : 0
                border.color: "#14151155"
            }
        }

        Text {
            renderType: Text.QtRendering
            anchors.horizontalCenter: parent.horizontalCenter
            width: parent.width
            text: root.label
            color: root.visual === "dormant" ? "#8a8e87" : "#141511"
            font.pixelSize: 15
            font.weight: Font.DemiBold
            elide: Text.ElideRight
            horizontalAlignment: Text.AlignHCenter
        }

        Text {
            renderType: Text.QtRendering
            anchors.horizontalCenter: parent.horizontalCenter
            width: parent.width
            text: root.caption
            color: "#8a8e87"
            font.pixelSize: 12
            horizontalAlignment: Text.AlignHCenter
        }
    }
}

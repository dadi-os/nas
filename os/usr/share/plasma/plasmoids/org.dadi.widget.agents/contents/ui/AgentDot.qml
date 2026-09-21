import QtQuick
import org.dadi.Desktop

Item {
    id: root
    property string visual: "idle"
    property string label: ""
    property string caption: "idle"

    readonly property real disc: visual === "dormant" ? 14 : 16
    readonly property real anchorY: disc / 2 + 2
    readonly property bool live: visual === "reasoning"
            || visual === "conversation"
            || visual === "both"

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
                visible: root.live
                anchors.centerIn: parent
                width: root.disc + 10
                height: width
                radius: width / 2
                color: "#14151118"
                SequentialAnimation on scale {
                    running: root.live
                    loops: Animation.Infinite
                    NumberAnimation { from: 0.9; to: 1.12; duration: 1200; easing.type: Easing.InOutSine }
                    NumberAnimation { from: 1.12; to: 0.9; duration: 1200; easing.type: Easing.InOutSine }
                }
            }

            Rectangle {
                visible: root.visual !== "reasoning"
                anchors.centerIn: parent
                width: root.disc
                height: width
                radius: width / 2
                color: root.visual === "dormant" ? "transparent" : Tokens.ink
                border.width: root.visual === "dormant" ? 1.5 : 0
                border.color: "#14151155"
            }

            Rectangle {
                visible: root.visual === "reasoning" || root.visual === "both"
                anchors.centerIn: parent
                width: root.visual === "both" ? root.disc * 0.55 : root.disc * 0.95
                height: width
                color: root.visual === "both" ? Tokens.bone : Tokens.ink
                border.width: 0
                rotation: 45
            }
        }

        Text {
            renderType: Text.QtRendering
            anchors.horizontalCenter: parent.horizontalCenter
            width: parent.width
            text: root.label
            color: root.visual === "dormant" ? Tokens.inkMuted : Tokens.ink
            font.pixelSize: Tokens.typeBody
            font.weight: Font.DemiBold
            elide: Text.ElideRight
            horizontalAlignment: Text.AlignHCenter
        }

        Text {
            renderType: Text.QtRendering
            anchors.horizontalCenter: parent.horizontalCenter
            width: parent.width
            text: root.caption
            color: Tokens.inkMuted
            font.pixelSize: Tokens.typeMeta
            horizontalAlignment: Text.AlignHCenter
        }
    }
}

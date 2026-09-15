import QtQuick
import QtQuick.Controls

/**
 * DadiButton is an ink-filled or ghost control with padded hit area.
 * kind is `primary`, `ghost`, or `danger`.
 */
Button {
    id: root

    property string kind: "primary"

    implicitHeight: 40
    leftPadding: 16
    rightPadding: 16
    topPadding: 0
    bottomPadding: 0
    hoverEnabled: true
    focusPolicy: Qt.TabFocus
    palette.accent: "#141511"
    palette.button: "#141511"
    palette.buttonText: "#ffffff"
    palette.highlight: "#141511"
    palette.highlightedText: "#ffffff"

    background: Rectangle {
        implicitHeight: 40
        implicitWidth: 96
        radius: 10
        color: {
            if (root.kind === "ghost") {
                if (!root.enabled)
                    return "transparent"
                if (root.down)
                    return "#14151114"
                if (root.hovered)
                    return "#1415110a"
                return "transparent"
            }
            if (root.kind === "danger") {
                if (!root.enabled)
                    return "#c45c4a55"
                return root.down ? "#a34b3d" : "#c45c4a"
            }
            if (!root.enabled)
                return "#14151118"
            return root.down ? "#000000" : "#141511"
        }
        border.width: root.kind === "ghost" ? 1 : (root.visualFocus ? 2 : 0)
        border.color: {
            if (root.kind === "ghost")
                return root.visualFocus ? "#141511" : "#14151122"
            return root.visualFocus ? "#ffffff" : "transparent"
        }
    }

    contentItem: Text {
        text: root.text
        color: {
            if (root.kind === "ghost")
                return root.enabled ? "#141511" : "#8a8e87"
            return "#ffffff"
        }
        font.pixelSize: 13
        font.weight: Font.DemiBold
        horizontalAlignment: Text.AlignHCenter
        verticalAlignment: Text.AlignVCenter
        elide: Text.ElideRight
    }
}

import QtQuick

Item {
    id: root
    property string visual: "idle"
    property bool isRoot: false
    property string label: ""

    readonly property real core: {
        if (visual === "dormant")
            return 4.5
        if (isRoot)
            return 8
        if (visual === "running")
            return 7
        return 6.5
    }

    width: 72
    height: 40

    Rectangle {
        id: halo
        visible: root.visual === "running"
        anchors.horizontalCenter: parent.horizontalCenter
        y: 2
        width: root.core * 3.4
        height: width
        radius: width / 2
        color: "#8fa38238"

        SequentialAnimation on opacity {
            running: halo.visible
            loops: Animation.Infinite
            NumberAnimation { from: 0.35; to: 0.85; duration: 900 }
            NumberAnimation { from: 0.85; to: 0.35; duration: 900 }
        }
    }

    Rectangle {
        id: dot
        anchors.horizontalCenter: parent.horizontalCenter
        y: 2 + (halo.visible ? (halo.height - width) / 2 : 4)
        width: root.core * 2
        height: width
        radius: width / 2
        color: {
            if (root.visual === "running")
                return "#8fa382"
            if (root.visual === "dormant")
                return "transparent"
            if (root.isRoot)
                return "#8fa38222"
            return "#fafaf7"
        }
        border.width: root.visual === "dormant" ? 1 : (root.isRoot ? 1.35 : 1)
        border.color: {
            if (root.visual === "running")
                return "#5c6b52"
            if (root.visual === "dormant")
                return "#b9c9ab8c"
            return "#8fa382b3"
        }
    }

    Text {
        anchors.horizontalCenter: parent.horizontalCenter
        anchors.top: dot.bottom
        anchors.topMargin: 3
        text: root.label
        color: root.visual === "dormant" ? "#b0b8a6" : (root.isRoot ? "#5c6b52" : "#6e7568")
        font.pixelSize: 8
        elide: Text.ElideRight
        width: 70
        horizontalAlignment: Text.AlignHCenter
    }
}

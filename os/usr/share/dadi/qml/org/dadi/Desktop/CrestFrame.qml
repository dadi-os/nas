import QtQuick
import QtQuick.Layouts
import org.dadi.Desktop

Item {
    id: root

    property string title: ""
    property string kicker: ""
    property string status: ""
    default property alias content: body.data
    property real frameRadius: 22

    implicitWidth: 320
    implicitHeight: 220

    Glass {
        id: glass
        anchors.fill: parent
        radius: root.frameRadius
        tint: "#ffffff"
        tintAlpha: 0.04
        blurRadius: 64
        fallbackOpacity: 0.28
        refractScale: 16
        chromaStrength: 0
    }

    Rectangle {
        anchors.fill: parent
        radius: root.frameRadius
        color: "transparent"
        border.color: "#ffffff"
        border.width: 1
        opacity: 0.38
        z: 1
    }

    RowLayout {
        id: header
        z: 3
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.top: parent.top
        anchors.leftMargin: 20
        anchors.rightMargin: 20
        anchors.topMargin: 16
        spacing: 10

        Text {
            text: root.title
            color: "#141511"
            font.pixelSize: Tokens.typeTitle
            font.weight: Font.DemiBold
            font.letterSpacing: -0.2
            renderType: Text.QtRendering
        }

        Item { Layout.fillWidth: true }

        Text {
            visible: root.kicker !== ""
            text: root.kicker
            color: "#8a8e87"
            font.pixelSize: Tokens.typeMeta
            renderType: Text.QtRendering
        }

        Text {
            visible: root.status !== ""
            text: root.status
            color: "#c45c4a"
            font.pixelSize: Tokens.typeMeta
            elide: Text.ElideRight
            Layout.maximumWidth: header.width * 0.4
            renderType: Text.QtRendering
        }
    }

    Item {
        id: body
        anchors.fill: parent
        anchors.topMargin: 48
        anchors.leftMargin: 18
        anchors.rightMargin: 18
        anchors.bottomMargin: 16
        clip: true
        z: 3
    }
}

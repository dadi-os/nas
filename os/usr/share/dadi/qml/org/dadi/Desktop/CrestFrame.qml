import QtQuick
import QtQuick.Layouts

Item {
    id: root

    property string title: ""
    default property alias content: body.data
    property real frameRadius: 16

    implicitWidth: 320
    implicitHeight: 220

    Glass {
        id: glass
        anchors.fill: parent
        anchors.margins: 2
        radius: root.frameRadius
        tint: "#fafaf7"
        tintAlpha: 0.38
        blurRadius: 24
        fallbackOpacity: 0.62
    }

    Rectangle {
        anchors.fill: parent
        anchors.margins: 2
        radius: root.frameRadius
        color: "transparent"
        border.color: "#b9c9ab"
        border.width: 1
        opacity: 0.55
        z: 1
    }

    Item {
        id: crest
        anchors.horizontalCenter: parent.horizontalCenter
        y: -10
        z: 2
        width: crestRow.width + 28
        height: 28

        Rectangle {
            anchors.centerIn: parent
            width: parent.width + 24
            height: parent.height + 16
            radius: width / 2
            gradient: Gradient {
                GradientStop { position: 0.0; color: "#fafaf7" }
                GradientStop { position: 0.55; color: "#fafaf7cc" }
                GradientStop { position: 1.0; color: "transparent" }
            }
        }

        Row {
            id: crestRow
            anchors.centerIn: parent
            spacing: 8

            Rectangle {
                width: 5
                height: 5
                rotation: 45
                anchors.verticalCenter: parent.verticalCenter
                color: "#8fa38255"
                border.color: "#b9c9ab"
                border.width: 1
            }

            Rectangle {
                radius: 3
                border.color: "#a8b89c"
                border.width: 1
                width: titleText.width + 18
                height: titleText.height + 8
                gradient: Gradient {
                    GradientStop { position: 0.0; color: "#fffef9" }
                    GradientStop { position: 0.5; color: "#fafaf7" }
                    GradientStop { position: 1.0; color: "#f7f9f4" }
                }

                Text {
                    id: titleText
                    anchors.centerIn: parent
                    text: root.title
                    color: "#5c6b52"
                    font.pixelSize: 10
                    font.weight: Font.Medium
                    font.letterSpacing: 2.4
                    font.capitalization: Font.AllUppercase
                }
            }

            Rectangle {
                width: 5
                height: 5
                rotation: 45
                anchors.verticalCenter: parent.verticalCenter
                color: "#8fa38255"
                border.color: "#b9c9ab"
                border.width: 1
            }
        }
    }

    Item {
        id: body
        anchors.fill: parent
        anchors.topMargin: 22
        anchors.leftMargin: 14
        anchors.rightMargin: 14
        anchors.bottomMargin: 12
        clip: true
        z: 3
    }
}

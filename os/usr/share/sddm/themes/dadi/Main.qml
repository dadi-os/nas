import QtQuick 2.15

Rectangle {
    id: root
    width: 640
    height: 480
    color: "#fafaf7"

    Image {
        anchors.fill: parent
        source: Qt.resolvedUrl("background.png")
        fillMode: Image.PreserveAspectCrop
    }

    Rectangle {
        anchors.fill: parent
        color: Qt.rgba(250 / 255, 250 / 255, 247 / 255, 0.35)
    }

    Image {
        anchors.horizontalCenter: parent.horizontalCenter
        anchors.verticalCenter: parent.verticalCenter
        source: Qt.resolvedUrl("wordmark.png")
        fillMode: Image.PreserveAspectFit
        width: Math.min(parent.width * 0.45, 420)
    }
}

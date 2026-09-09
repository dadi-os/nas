import QtQuick 2.15

Rectangle {
    id: root
    width: 640
    height: 480
    color: "#fafaf7"

    property int sessionIndex: sessionModel.lastIndex

    Image {
        anchors.fill: parent
        source: Qt.resolvedUrl("background.png")
        fillMode: Image.PreserveAspectCrop
        opacity: 1
    }

    // Soft veil over leaf field
    Rectangle {
        anchors.fill: parent
        color: Qt.rgba(250 / 255, 250 / 255, 247 / 255, 0.35)
    }

    Image {
        id: wordmark
        anchors.horizontalCenter: parent.horizontalCenter
        anchors.verticalCenter: parent.verticalCenter
        anchors.verticalCenterOffset: -48
        source: Qt.resolvedUrl("wordmark.png")
        fillMode: Image.PreserveAspectFit
        width: Math.min(parent.width * 0.45, 420)
    }

    Rectangle {
        id: field
        anchors.horizontalCenter: parent.horizontalCenter
        anchors.top: wordmark.bottom
        anchors.topMargin: 32
        width: 280
        height: 36
        radius: 9
        color: Qt.rgba(250 / 255, 250 / 255, 247 / 255, 0.78)
        border.color: password.activeFocus ? "#8fa382" : "#b9c9ab"
        border.width: 1

        TextInput {
            id: password
            anchors.fill: parent
            anchors.margins: 8
            echoMode: TextInput.Password
            color: "#2c302a"
            font.family: "Noto Sans"
            font.pixelSize: 14
            focus: true
            Keys.onPressed: function (event) {
                if (event.key === Qt.Key_Return || event.key === Qt.Key_Enter) {
                    sddm.login("ankur", password.text, root.sessionIndex)
                    event.accepted = true
                }
            }
        }
    }

    Text {
        anchors.horizontalCenter: parent.horizontalCenter
        anchors.top: field.bottom
        anchors.topMargin: 12
        text: "ankur"
        color: "#6e7568"
        font.family: "Noto Sans"
        font.pixelSize: 12
    }

    Connections {
        target: sddm
        function onLoginFailed() {
            password.clear()
        }
    }

    Component.onCompleted: password.forceActiveFocus()
}

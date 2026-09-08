import QtQuick
import QtQuick.Controls
import QtQuick.Layouts

Item {
    id: root
    signal saved(string message)

    property string token: ""
    property string status: ""

    function load() {
        const xhr = new XMLHttpRequest()
        xhr.onreadystatechange = function () {
            if (xhr.readyState !== XMLHttpRequest.DONE)
                return
            if (xhr.status === 200)
                token = xhr.responseText
            else
                status = "Failed to load token"
        }
        xhr.open("GET", "http://127.0.0.1:8092/cloudflared/token")
        xhr.send()
    }

    function save() {
        status = "Saving…"
        const xhr = new XMLHttpRequest()
        xhr.onreadystatechange = function () {
            if (xhr.readyState !== XMLHttpRequest.DONE)
                return
            if (xhr.status !== 200) {
                status = "Save failed (" + xhr.status + ")"
                return
            }
            status = ""
            root.saved("tunnel token saved · cloudflared restarted")
        }
        xhr.open("PUT", "http://127.0.0.1:8092/cloudflared/token")
        xhr.setRequestHeader("Content-Type", "text/plain; charset=utf-8")
        xhr.send(token)
    }

    Component.onCompleted: load()

    ColumnLayout {
        anchors.fill: parent
        spacing: 12

        Text {
            text: "Tunnel"
            color: "#2c302a"
            font.pixelSize: 22
            font.weight: Font.DemiBold
        }
        Text {
            text: "Cloudflare tunnel token for Headscale exposure. Stored under /var/lib/dadi/cloudflared."
            color: "#6e7568"
            font.pixelSize: 12
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        ScrollView {
            Layout.fillWidth: true
            Layout.fillHeight: true
            TextArea {
                width: parent.availableWidth
                wrapMode: TextEdit.Wrap
                text: root.token
                onTextChanged: root.token = text
                font.family: "Noto Sans Mono"
                font.pixelSize: 12
                color: "#2c302a"
                background: Rectangle {
                    radius: 9
                    color: "#f7f9f4"
                    border.color: "#b9c9ab"
                    border.width: 1
                }
            }
        }

        Button {
            text: "Save token"
            onClicked: root.save()
            background: Rectangle {
                radius: 9
                color: parent.down ? "#5c6b52" : "#8fa382"
            }
            contentItem: Text {
                text: parent.text
                color: "#fafaf7"
                horizontalAlignment: Text.AlignHCenter
                verticalAlignment: Text.AlignVCenter
            }
        }

        Text {
            text: status
            color: "#6e7568"
            font.pixelSize: 11
            visible: status !== ""
        }
    }
}

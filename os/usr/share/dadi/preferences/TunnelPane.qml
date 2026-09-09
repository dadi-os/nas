import QtQuick
import QtQuick.Controls
import QtQuick.Layouts

Item {
    id: root
    signal saved(string message)

    property string token: ""
    property string controlUrl: ""
    property string status: ""

    function load() {
        const tokenXhr = new XMLHttpRequest()
        tokenXhr.onreadystatechange = function () {
            if (tokenXhr.readyState !== XMLHttpRequest.DONE)
                return
            if (tokenXhr.status === 200)
                token = tokenXhr.responseText
            else
                status = "Failed to load token"
        }
        tokenXhr.open("GET", "http://127.0.0.1:8092/cloudflared/token")
        tokenXhr.send()

        const urlXhr = new XMLHttpRequest()
        urlXhr.onreadystatechange = function () {
            if (urlXhr.readyState !== XMLHttpRequest.DONE)
                return
            if (urlXhr.status === 200)
                controlUrl = urlXhr.responseText.trim()
            else if (status === "")
                status = "Failed to load control URL"
        }
        urlXhr.open("GET", "http://127.0.0.1:8092/headscale/control-url")
        urlXhr.send()
    }

    function saveToken() {
        status = "Saving token…"
        const xhr = new XMLHttpRequest()
        xhr.onreadystatechange = function () {
            if (xhr.readyState !== XMLHttpRequest.DONE)
                return
            if (xhr.status !== 200) {
                status = "Token save failed (" + xhr.status + ")"
                return
            }
            status = ""
            root.saved("tunnel token saved · cloudflared restarted")
        }
        xhr.open("PUT", "http://127.0.0.1:8092/cloudflared/token")
        xhr.setRequestHeader("Content-Type", "text/plain; charset=utf-8")
        xhr.send(token)
    }

    function saveControlUrl() {
        status = "Saving control URL…"
        const xhr = new XMLHttpRequest()
        xhr.onreadystatechange = function () {
            if (xhr.readyState !== XMLHttpRequest.DONE)
                return
            if (xhr.status !== 200) {
                status = "Control URL save failed (" + xhr.status + ")"
                return
            }
            status = ""
            root.saved("control URL saved · used in device provision bundles")
        }
        xhr.open("PUT", "http://127.0.0.1:8092/headscale/control-url")
        xhr.setRequestHeader("Content-Type", "text/plain; charset=utf-8")
        xhr.send(controlUrl)
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
            text: "Headscale control plane URL (embedded in device QR codes) and Cloudflare tunnel token."
            color: "#6e7568"
            font.pixelSize: 12
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        Text {
            text: "Control plane URL"
            color: "#2c302a"
            font.pixelSize: 13
            font.weight: Font.DemiBold
        }
        Text {
            text: "Public Headscale URL clients dial when joining dadiMesh (e.g. https://dadi.ardusa.dev)."
            color: "#6e7568"
            font.pixelSize: 12
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }
        TextField {
            Layout.fillWidth: true
            text: root.controlUrl
            onTextChanged: root.controlUrl = text
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
        Button {
            text: "Save control URL"
            onClicked: root.saveControlUrl()
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
            text: "Cloudflare tunnel token"
            color: "#2c302a"
            font.pixelSize: 13
            font.weight: Font.DemiBold
            Layout.topMargin: 8
        }
        Text {
            text: "Stored under /var/lib/dadi/cloudflared."
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
            onClicked: root.saveToken()
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

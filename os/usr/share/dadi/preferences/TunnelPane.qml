import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.dadi.Desktop

Item {
    id: root
    signal saved(string message)

    property string controlUrl: ""
    property string wanIp: ""
    property string lanIp: ""
    property string status: ""

    function load() {
        status = ""
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

        const pubXhr = new XMLHttpRequest()
        pubXhr.onreadystatechange = function () {
            if (pubXhr.readyState !== XMLHttpRequest.DONE)
                return
            if (pubXhr.status !== 200) {
                if (status === "")
                    status = "Failed to load WAN/LAN"
                return
            }
            try {
                const st = JSON.parse(pubXhr.responseText)
                wanIp = st.wan_ip || ""
                lanIp = st.lan_ip || ""
            } catch (e) {
                if (status === "")
                    status = "Failed to parse publish status"
            }
        }
        pubXhr.open("GET", "http://127.0.0.1:8092/headscale/publish")
        pubXhr.send()
    }

    function saveControlUrl() {
        status = "Publishing…"
        const xhr = new XMLHttpRequest()
        xhr.onreadystatechange = function () {
            if (xhr.readyState !== XMLHttpRequest.DONE)
                return
            if (xhr.status !== 200) {
                status = "Publish failed (" + xhr.status + ")"
                try {
                    const payload = JSON.parse(xhr.responseText)
                    if (payload.error && payload.error.message)
                        status = payload.error.message
                } catch (e) {
                    status = "Publish failed (" + xhr.status + ")"
                }
                return
            }
            status = ""
            try {
                const st = JSON.parse(xhr.responseText)
                wanIp = st.wan_ip || wanIp
                lanIp = st.lan_ip || lanIp
            } catch (e) {
                status = "Published, but the response was not JSON"
            }
            root.saved("control plane published · UPnP mapped 80/443")
            load()
        }
        xhr.open("PUT", "http://127.0.0.1:8092/headscale/control-url")
        xhr.setRequestHeader("Content-Type", "text/plain; charset=utf-8")
        xhr.send(controlUrl)
    }

    Component.onCompleted: load()

    ColumnLayout {
        anchors.fill: parent
        spacing: 14

        Text {
            text: "Tunnel"
            color: "#141511"
            font.pixelSize: 22
            font.weight: Font.DemiBold
            font.letterSpacing: -0.3
        }
        Text {
            text: "Public Headscale URL Hath dials to join dadiMesh. Saving maps ports 80 and 443 on the router via UPnP and serves HTTPS on this box. Point the hostname’s DNS A record at the WAN address."
            color: "#8a8e87"
            font.pixelSize: 13
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        Text {
            text: "Control plane URL"
            color: "#141511"
            font.pixelSize: 13
            font.weight: Font.DemiBold
        }
        Text {
            text: "https://hostname — Let’s Encrypt needs this name to resolve to the WAN IP below."
            color: "#8a8e87"
            font.pixelSize: 12
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }
        TextField {
            Layout.fillWidth: true
            Layout.preferredHeight: 40
            leftPadding: 12
            rightPadding: 12
            text: root.controlUrl
            onTextChanged: root.controlUrl = text
            font.family: "Noto Sans Mono"
            font.pixelSize: 13
            color: "#141511"
            selectByMouse: true
            background: Rectangle {
                radius: 10
                color: "#ffffffcc"
                border.color: parent.activeFocus ? "#141511" : "#14151122"
                border.width: 1
            }
        }
        DadiButton {
            text: "Save and publish"
            Layout.alignment: Qt.AlignLeft
            onClicked: root.saveControlUrl()
        }

        Text {
            visible: root.wanIp !== "" || root.lanIp !== ""
            text: (root.wanIp !== "" ? ("WAN " + root.wanIp) : "") + (root.lanIp !== "" ? ("  ·  LAN " + root.lanIp) : "")
            color: "#141511"
            font.pixelSize: 13
            font.family: "Noto Sans Mono"
        }

        Text {
            text: status
            color: "#c45c4a"
            font.pixelSize: 12
            visible: status !== ""
        }

        Item { Layout.fillHeight: true }
    }
}

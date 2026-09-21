import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.dadi.Desktop

Item {
    id: root
    signal saved(string message)

    property string status: ""

    function load() {
        status = ""
        const xhr = new XMLHttpRequest()
        xhr.onreadystatechange = function () {
            if (xhr.readyState !== XMLHttpRequest.DONE)
                return
            if (xhr.status !== 200) {
                status = "Failed to load chaavi settings (" + xhr.status + ")"
                return
            }
            try {
                apply(JSON.parse(xhr.responseText))
            } catch (e) {
                status = "Bad settings payload"
            }
        }
        xhr.open("GET", "http://127.0.0.1:8092/modules/chaavi/settings")
        xhr.send()
    }

    function apply(data) {
        const env = data.env || {}
        clientId.text = env.BW_CLIENTID || ""
        clientSecret.text = env.BW_CLIENTSECRET || ""
        password.text = env.BW_PASSWORD || ""
    }

    function payload() {
        return {
            env: {
                BW_CLIENTID: clientId.text,
                BW_CLIENTSECRET: clientSecret.text,
                BW_PASSWORD: password.text
            }
        }
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
            root.saved("chaavi saved · restarted")
        }
        xhr.open("PUT", "http://127.0.0.1:8092/modules/chaavi/settings")
        xhr.setRequestHeader("Content-Type", "application/json")
        xhr.send(JSON.stringify(payload()))
    }

    Component.onCompleted: load()
    onVisibleChanged: if (visible) load()

    DadiFlickable {
        id: flick
        anchors.fill: parent
        contentWidth: width
        contentHeight: col.height
        ScrollBar.vertical: ScrollBar {
            policy: ScrollBar.AsNeeded
            contentItem: Rectangle {
                implicitWidth: 6
                radius: 3
                color: "#14151133"
            }
        }

        ColumnLayout {
            id: col
            width: parent.width
            spacing: 14

            Text {
                text: "Chaavi"
                color: "#141511"
                font.pixelSize: 22
                font.weight: Font.DemiBold
                font.letterSpacing: -0.3
            }
            Text {
                text: "Bitwarden personal API key and master password for the account at https://chaavi.dadi. Empty is fine — Chaavi boots without them. Saving restarts chaavi."
                color: "#8a8e87"
                font.pixelSize: 13
                wrapMode: Text.WordWrap
                Layout.fillWidth: true
            }

            Text { text: "Keys"; color: "#8a8e87"; font.pixelSize: 12; Layout.topMargin: 4 }
            FormRow { id: clientId; label: "Client ID"; secret: true }
            FormRow { id: clientSecret; label: "Client secret"; secret: true }
            FormRow { id: password; label: "Master password"; secret: true }

            DadiButton {
                text: "Save"
                Layout.topMargin: 8
                Layout.alignment: Qt.AlignLeft
                onClicked: root.save()
            }

            Text {
                text: status
                color: "#c45c4a"
                font.pixelSize: 12
                visible: status !== ""
                wrapMode: Text.WordWrap
                Layout.fillWidth: true
            }

            Item { Layout.preferredHeight: 8 }
        }
    }
}

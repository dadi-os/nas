import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.dadi.Desktop

Item {
    id: root
    signal saved(string message)

    property string status: ""

    Http { id: api }

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
                status = "Bad settings payload: " + e.message
            }
        }
        xhr.open("GET", "http://127.0.0.1:8092/modules/chaavi/settings")
        xhr.send()
    }

    function apply(data) {
        const env = data.env || {}
        email.text = env.CHAAVI_EMAIL || ""
        password.text = env.CHAAVI_PASSWORD || ""
    }

    function payload() {
        return {
            env: {
                CHAAVI_EMAIL: email.text,
                CHAAVI_PASSWORD: password.text
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
                status = "Save failed: " + api.errorText(xhr.status, xhr.responseText)
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
                text: "The vault account at https://chaavi.dadi that Chaavi unlocks for the agents: its email and master password. Empty is fine — Chaavi boots without them, and with only one account it finds the email itself. Saving restarts chaavi."
                color: "#8a8e87"
                font.pixelSize: 13
                wrapMode: Text.WordWrap
                Layout.fillWidth: true
            }

            Text { text: "Account"; color: "#8a8e87"; font.pixelSize: 12; Layout.topMargin: 4 }
            FormRow { id: email; label: "Email" }
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

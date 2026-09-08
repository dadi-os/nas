pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.plasma.plasmoid
import org.kde.plasma.core as PlasmaCore

PlasmoidItem {
    id: root

    preferredRepresentation: fullRepresentation
    toolTipMainText: "Add Device"
    Plasmoid.backgroundHints: PlasmaCore.Types.NoBackground

    fullRepresentation: Rectangle {
        id: win
        Layout.minimumWidth: 420
        Layout.minimumHeight: 560
        Layout.preferredWidth: 460
        Layout.preferredHeight: 640
        color: "#fafaf7"

        property string nodeName: ""
        property string bundle: ""
        property string qrPath: ""
        property string status: ""
        property bool busy: false
        property bool copied: false

        PlasmaCore.DataSource {
            id: executable
            engine: "executable"
            connectedSources: []
            property var callbacks: ({})
            onNewData: function (source, data) {
                const cb = callbacks[source]
                disconnectSource(source)
                delete callbacks[source]
                if (cb)
                    cb(data["exit code"], data.stdout || "", data.stderr || "")
            }
            function run(cmd, cb) {
                callbacks[cmd] = cb
                connectSource(cmd)
            }
        }

        function shellQuote(s) {
            return "'" + String(s).replace(/'/g, "'\\''") + "'"
        }

        function mint() {
            const name = nodeName.trim()
            if (name === "" || busy)
                return
            busy = true
            status = ""
            bundle = ""
            qrPath = ""
            copied = false

            const xhr = new XMLHttpRequest()
            xhr.onreadystatechange = function () {
                if (xhr.readyState !== XMLHttpRequest.DONE)
                    return
                busy = false
                if (xhr.status !== 200) {
                    status = "Provision failed (" + xhr.status + ")"
                    return
                }
                try {
                    const res = JSON.parse(xhr.responseText)
                    bundle = res.bundle || ""
                    if (bundle === "") {
                        status = "Empty bundle"
                        return
                    }
                    renderQr(bundle)
                } catch (e) {
                    status = "Bad response"
                }
            }
            xhr.open("POST", "http://127.0.0.1:8092/provision")
            xhr.setRequestHeader("Content-Type", "application/json")
            xhr.send(JSON.stringify({ node_name: name }))
        }

        function renderQr(text) {
            const out = "/tmp/dadi-add-device-qr.png"
            executable.run(
                "dadi-provision-qr " + shellQuote(text) + " " + out,
                function (code) {
                    if (code === 0)
                        qrPath = "file://" + out + "?t=" + Date.now()
                    else
                        status = "QR render failed"
                }
            )
        }

        function copyBundle() {
            if (bundle === "")
                return
            const cmd = "printf %s " + shellQuote(bundle) + " | wl-copy"
            executable.run("bash -lc " + shellQuote(cmd), function () {
                copied = true
            })
        }

        ColumnLayout {
            anchors.fill: parent
            anchors.margins: 28
            spacing: 16

            Text {
                text: "દાદી"
                color: "#7e9270"
                font.family: "Noto Sans Gujarati"
                font.pixelSize: 36
                font.weight: Font.Medium
                Layout.alignment: Qt.AlignHCenter
            }

            Text {
                text: "ADD DEVICE"
                color: "#a8af9f"
                font.pixelSize: 11
                font.letterSpacing: 2.5
                Layout.alignment: Qt.AlignHCenter
            }

            Text {
                text: "Name the new Hath, then show the QR. Single-use — expires in about an hour."
                color: "#6e7568"
                font.pixelSize: 13
                wrapMode: Text.WordWrap
                horizontalAlignment: Text.AlignHCenter
                Layout.fillWidth: true
            }

            Rectangle {
                Layout.fillWidth: true
                Layout.preferredHeight: 48
                radius: 12
                color: "#f7f9f4"
                border.color: nameField.activeFocus ? "#8fa382" : "#b9c9ab"
                border.width: 1

                TextInput {
                    id: nameField
                    anchors.fill: parent
                    anchors.margins: 12
                    text: win.nodeName
                    onTextChanged: win.nodeName = text
                    color: "#2c302a"
                    font.pixelSize: 14
                    selectByMouse: true
                    clip: true
                    Keys.onReturnPressed: win.mint()
                }

                Text {
                    anchors.fill: parent
                    anchors.margins: 12
                    text: "ankur-phone"
                    color: "#b0b8a6"
                    font.pixelSize: 14
                    visible: nameField.text.length === 0 && !nameField.activeFocus
                }
            }

            Button {
                Layout.fillWidth: true
                Layout.preferredHeight: 42
                enabled: !win.busy && win.nodeName.trim().length > 0
                onClicked: win.mint()
                background: Rectangle {
                    radius: 12
                    color: parent.down ? "#5c6b52" : (parent.enabled ? "#8fa382" : "#d5ddcb")
                }
                contentItem: Text {
                    text: win.busy ? "CREATING…" : "CREATE SETUP CODE"
                    color: "#fafaf7"
                    font.pixelSize: 12
                    font.letterSpacing: 1.5
                    horizontalAlignment: Text.AlignHCenter
                    verticalAlignment: Text.AlignVCenter
                }
            }

            Item {
                Layout.fillWidth: true
                Layout.fillHeight: true
                Layout.minimumHeight: 260

                Rectangle {
                    anchors.centerIn: parent
                    width: 260
                    height: 260
                    radius: 16
                    color: "#fafaf7"
                    border.color: "#b9c9ab"
                    border.width: 1

                    Image {
                        anchors.centerIn: parent
                        width: 240
                        height: 240
                        source: win.qrPath
                        fillMode: Image.PreserveAspectFit
                        visible: win.qrPath !== ""
                        cache: false
                    }

                    Text {
                        anchors.centerIn: parent
                        visible: win.qrPath === ""
                        text: win.busy ? "…" : "QR appears here"
                        color: "#b0b8a6"
                        font.pixelSize: 13
                    }
                }
            }

            Button {
                Layout.alignment: Qt.AlignHCenter
                visible: win.bundle !== ""
                text: win.copied ? "Copied" : "Copy setup code"
                flat: true
                onClicked: win.copyBundle()
                contentItem: Text {
                    text: parent.text
                    color: "#5c6b52"
                    font.pixelSize: 12
                    font.letterSpacing: 1.2
                    horizontalAlignment: Text.AlignHCenter
                }
            }

            Text {
                text: win.status
                color: "#b56b5c"
                font.pixelSize: 12
                visible: win.status !== ""
                Layout.alignment: Qt.AlignHCenter
                wrapMode: Text.WordWrap
                Layout.fillWidth: true
                horizontalAlignment: Text.AlignHCenter
            }
        }
    }
}

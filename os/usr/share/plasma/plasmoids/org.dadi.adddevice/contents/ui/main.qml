pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.plasma.plasmoid
import org.kde.plasma.core as PlasmaCore
import org.kde.plasma.plasma5support as Plasma5Support

PlasmoidItem {
    id: root

    preferredRepresentation: fullRepresentation
    toolTipMainText: "Add Device"
    Plasmoid.backgroundHints: PlasmaCore.Types.NoBackground

    fullRepresentation: Rectangle {
        id: win
        Layout.minimumWidth: 380
        Layout.minimumHeight: 520
        Layout.preferredWidth: 420
        Layout.preferredHeight: 580
        color: "#fafaf7"

        property string nodeName: ""
        property string bundle: ""
        property string qrPath: ""
        property string status: ""
        property bool busy: false
        property bool copied: false

        Plasma5Support.DataSource {
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
            anchors.leftMargin: 24
            anchors.rightMargin: 24
            anchors.topMargin: 20
            anchors.bottomMargin: 20
            spacing: 12

            Text {
                text: "Add device"
                color: "#2c302a"
                font.pixelSize: 20
                font.weight: Font.DemiBold
            }
            Text {
                text: "Name the Hath, then scan the QR. Single-use, about an hour."
                color: "#6e7568"
                font.pixelSize: 13
                wrapMode: Text.WordWrap
                Layout.fillWidth: true
            }

            Text {
                text: "Name"
                color: "#2c302a"
                font.pixelSize: 13
            }
            Rectangle {
                Layout.fillWidth: true
                Layout.preferredHeight: 40
                radius: 9
                color: "#f7f9f4"
                border.color: nameField.activeFocus ? "#8fa382" : "#b9c9ab"
                border.width: 1

                TextInput {
                    id: nameField
                    anchors.fill: parent
                    anchors.leftMargin: 12
                    anchors.rightMargin: 12
                    verticalAlignment: Text.AlignVCenter
                    text: win.nodeName
                    onTextChanged: win.nodeName = text
                    color: "#2c302a"
                    font.pixelSize: 13
                    selectByMouse: true
                    clip: true
                    Keys.onReturnPressed: win.mint()
                }

                Text {
                    anchors.fill: parent
                    anchors.leftMargin: 12
                    text: "phone"
                    color: "#b0b8a6"
                    font.pixelSize: 13
                    verticalAlignment: Text.AlignVCenter
                    visible: nameField.text.length === 0 && !nameField.activeFocus
                }
            }

            Button {
                Layout.fillWidth: true
                Layout.preferredHeight: 40
                enabled: !win.busy && win.nodeName.trim().length > 0
                onClicked: win.mint()
                background: Rectangle {
                    radius: 9
                    color: parent.down ? "#5c6b52" : (parent.enabled ? "#8fa382" : "#d5ddcb")
                }
                contentItem: Text {
                    text: win.busy ? "Creating…" : "Create setup code"
                    color: "#fafaf7"
                    font.pixelSize: 13
                    horizontalAlignment: Text.AlignHCenter
                    verticalAlignment: Text.AlignVCenter
                }
            }

            Item {
                Layout.fillWidth: true
                Layout.fillHeight: true

                Rectangle {
                    anchors.centerIn: parent
                    width: Math.min(parent.width, parent.height, 220)
                    height: width
                    radius: 12
                    color: "#f7f9f4"
                    border.color: "#b9c9ab"
                    border.width: 1

                    Image {
                        anchors.fill: parent
                        anchors.margins: 10
                        source: win.qrPath
                        fillMode: Image.PreserveAspectFit
                        visible: win.qrPath !== ""
                        cache: false
                    }

                    Text {
                        anchors.centerIn: parent
                        visible: win.qrPath === ""
                        text: win.busy ? "…" : "QR"
                        color: "#b0b8a6"
                        font.pixelSize: 13
                    }
                }
            }

            Button {
                Layout.alignment: Qt.AlignHCenter
                visible: win.bundle !== ""
                flat: true
                onClicked: win.copyBundle()
                contentItem: Text {
                    text: win.copied ? "Copied" : "Copy setup code"
                    color: "#5c6b52"
                    font.pixelSize: 13
                    horizontalAlignment: Text.AlignHCenter
                }
            }

            Text {
                text: win.status
                color: "#b56b5c"
                font.pixelSize: 12
                visible: win.status !== ""
                Layout.fillWidth: true
                wrapMode: Text.WordWrap
            }
        }
    }
}

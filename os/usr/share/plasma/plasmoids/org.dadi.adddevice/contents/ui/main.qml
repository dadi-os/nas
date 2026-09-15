pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.plasma.plasmoid
import org.kde.plasma.core as PlasmaCore
import org.kde.plasma.plasma5support as Plasma5Support
import org.dadi.Desktop

PlasmoidItem {
    id: root

    preferredRepresentation: fullRepresentation
    toolTipMainText: "Add Device"
    Plasmoid.backgroundHints: PlasmaCore.Types.NoBackground

    fullRepresentation: Item {
        id: win
        Layout.minimumWidth: 760
        Layout.minimumHeight: 420
        Layout.preferredWidth: 820
        Layout.preferredHeight: 460

        property string nodeName: ""
        property string bundle: ""
        property string qrPath: ""
        property string status: ""
        property bool busy: false
        property bool copied: false

        FrostShell { anchors.fill: parent }

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
            xhr.open("POST", Tokens.nasBase + "/provision")
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

        RowLayout {
            anchors.fill: parent
            anchors.leftMargin: 28
            anchors.rightMargin: 28
            anchors.topMargin: 24
            anchors.bottomMargin: 24
            spacing: 32

            ColumnLayout {
                Layout.preferredWidth: 280
                Layout.maximumWidth: 320
                Layout.fillHeight: true
                spacing: 12

                Text {
                    text: "Add device"
                    color: "#141511"
                    font.pixelSize: 22
                    font.weight: Font.DemiBold
                    font.letterSpacing: -0.3
                }
                Text {
                    text: "Name the Hath, then scan the QR. Single-use, about an hour."
                    color: "#8a8e87"
                    font.pixelSize: 13
                    wrapMode: Text.WordWrap
                    Layout.fillWidth: true
                }

                Text {
                    text: "Name"
                    color: "#141511"
                    font.pixelSize: 13
                    Layout.topMargin: 8
                }
                Rectangle {
                    Layout.fillWidth: true
                    Layout.preferredHeight: 40
                    radius: 10
                    color: "#ffffffcc"
                    border.color: nameField.activeFocus ? "#141511" : "#14151122"
                    border.width: 1

                    TextInput {
                        id: nameField
                        anchors.fill: parent
                        anchors.leftMargin: 12
                        anchors.rightMargin: 12
                        verticalAlignment: Text.AlignVCenter
                        text: win.nodeName
                        onTextChanged: win.nodeName = text
                        color: "#141511"
                        font.pixelSize: 13
                        selectByMouse: true
                        clip: true
                        Keys.onReturnPressed: win.mint()
                    }

                    Text {
                        anchors.fill: parent
                        anchors.leftMargin: 12
                        text: "phone"
                        color: "#8a8e87"
                        font.pixelSize: 13
                        verticalAlignment: Text.AlignVCenter
                        visible: nameField.text.length === 0 && !nameField.activeFocus
                    }
                }

                DadiButton {
                    Layout.fillWidth: true
                    text: win.busy ? "Creating…" : "Create setup code"
                    enabled: !win.busy && win.nodeName.trim().length > 0
                    onClicked: win.mint()
                }

                DadiButton {
                    Layout.fillWidth: true
                    kind: "ghost"
                    visible: win.bundle !== ""
                    text: win.copied ? "Copied" : "Copy setup code"
                    onClicked: win.copyBundle()
                }

                Text {
                    text: win.status
                    color: "#c45c4a"
                    font.pixelSize: 12
                    visible: win.status !== ""
                    Layout.fillWidth: true
                    wrapMode: Text.WordWrap
                }

                Item { Layout.fillHeight: true }
            }

            Item {
                Layout.fillWidth: true
                Layout.fillHeight: true
                Layout.minimumWidth: 300

                Rectangle {
                    id: qrPlate
                    readonly property int side: Math.min(parent.width, parent.height)
                    width: side
                    height: side
                    anchors.centerIn: parent
                    radius: 16
                    color: "#ffffff"
                    border.color: "#14151114"
                    border.width: 1

                    Image {
                        anchors.fill: parent
                        anchors.margins: 16
                        source: win.qrPath
                        fillMode: Image.PreserveAspectFit
                        visible: win.qrPath !== ""
                        cache: false
                    }

                    Text {
                        anchors.centerIn: parent
                        visible: win.qrPath === ""
                        text: win.busy ? "Creating…" : "QR appears here"
                        color: "#8a8e87"
                        font.pixelSize: 13
                    }
                }
            }
        }
    }
}

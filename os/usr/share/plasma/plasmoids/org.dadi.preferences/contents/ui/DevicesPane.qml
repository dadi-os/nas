import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.plasma.plasma5support as Plasma5Support
import org.dadi.Desktop

Item {
    id: root

    property var clients: []
    property string bundle: ""
    property string qrPath: ""
    property string status: ""
    property bool busy: false
    property bool copied: false
    property var reserved: []

    readonly property string draftName: nameField.text.trim()
    readonly property bool nameTaken: nameIsTaken(draftName)

    function nameIsTaken(name) {
        const want = name.toLowerCase()
        if (want === "")
            return false
        for (let i = 0; i < clients.length; i++) {
            if (String(clients[i].node_name).toLowerCase() === want)
                return true
        }
        for (let i = 0; i < reserved.length; i++) {
            if (String(reserved[i]).toLowerCase() === want)
                return true
        }
        return false
    }

    Http { id: api }

    function load() {
        api.get(Tokens.nasBase + "/clients", function (code, body) {
            if (code !== 200) {
                status = "Failed to load devices (" + code + ")"
                return
            }
            try {
                const data = JSON.parse(body)
                if (!Array.isArray(data.clients)) {
                    status = "Bad devices payload"
                    return
                }
                clients = data.clients
            } catch (e) {
                status = "Bad devices payload"
            }
        })
    }

    function mint() {
        const name = draftName
        if (name === "" || busy || nameTaken)
            return
        busy = true
        status = ""
        bundle = ""
        qrPath = ""
        copied = false
        api.postJson(Tokens.nasBase + "/provision", { node_name: name }, function (code, body) {
            busy = false
            if (code !== 200) {
                try {
                    const data = JSON.parse(body)
                    status = (data.error && data.error.message)
                            ? data.error.message
                            : ("Provision failed (" + code + ")")
                    if (code === 409 && !nameIsTaken(name))
                        reserved = reserved.concat([name])
                } catch (e) {
                    status = "Provision failed (" + code + ")"
                }
                return
            }
            try {
                const res = JSON.parse(body)
                bundle = res.bundle || ""
                if (bundle === "") {
                    status = "Empty bundle"
                    return
                }
                reserved = reserved.concat([name])
                renderQr(bundle)
                load()
            } catch (e) {
                status = "Bad response"
            }
        })
    }

    function shellQuote(s) {
        return "'" + String(s).replace(/'/g, "'\\''") + "'"
    }

    function renderQr(text) {
        const out = "/tmp/dadi-device-qr.png"
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
        executable.run("bash -lc " + shellQuote(cmd), function (code) {
            if (code === 0)
                copied = true
            else
                status = "Copy failed"
        })
    }

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

    Component.onCompleted: load()
    onVisibleChanged: if (visible) load()

    RowLayout {
        anchors.fill: parent
        spacing: 32

        ColumnLayout {
            Layout.preferredWidth: 340
            Layout.maximumWidth: 380
            Layout.fillHeight: true
            spacing: 12

            Text {
                text: "Devices"
                color: "#141511"
                font.pixelSize: 22
                font.weight: Font.DemiBold
                font.letterSpacing: -0.3
            }
            Text {
                text: "Name a Hath, then scan the QR from that device. Setup codes are single-use and last about an hour."
                color: "#8a8e87"
                font.pixelSize: 13
                wrapMode: Text.WordWrap
                Layout.fillWidth: true
            }

            FormRow {
                id: nameField
                label: "Name"
                hint: "Shown on the mesh as this hostname."
            }

            Text {
                visible: root.nameTaken
                text: "“" + root.draftName + "” is already taken"
                color: "#c45c4a"
                font.pixelSize: 12
                wrapMode: Text.WordWrap
                Layout.fillWidth: true
            }

            DadiButton {
                Layout.fillWidth: true
                text: root.busy ? "Creating…" : "Create setup code"
                enabled: !root.busy && root.draftName.length > 0 && !root.nameTaken
                onClicked: root.mint()
            }
            DadiButton {
                Layout.fillWidth: true
                kind: "ghost"
                visible: root.bundle !== ""
                text: root.copied ? "Copied" : "Copy setup code"
                onClicked: root.copyBundle()
            }

            Text {
                text: root.status
                color: "#c45c4a"
                font.pixelSize: 12
                visible: root.status !== ""
                wrapMode: Text.WordWrap
                Layout.fillWidth: true
            }

            Text {
                text: "On the mesh"
                color: "#141511"
                font.pixelSize: 13
                font.weight: Font.DemiBold
                Layout.topMargin: 8
            }

            Flickable {
                Layout.fillWidth: true
                Layout.fillHeight: true
                contentWidth: width
                contentHeight: roster.height
                clip: true
                boundsBehavior: Flickable.StopAtBounds

                ColumnLayout {
                    id: roster
                    width: parent.width
                    spacing: 4

                    Text {
                        visible: root.clients.length === 0
                        text: "No devices yet."
                        color: "#8a8e87"
                        font.pixelSize: 13
                    }

                    Repeater {
                        model: root.clients
                        delegate: Rectangle {
                            id: row
                            required property var modelData
                            Layout.fillWidth: true
                            implicitHeight: 40
                            radius: 10
                            color: "transparent"

                            RowLayout {
                                anchors.fill: parent
                                anchors.leftMargin: 10
                                anchors.rightMargin: 10
                                spacing: 10

                                Rectangle {
                                    width: 7
                                    height: 7
                                    radius: 4
                                    color: row.modelData.online ? "#141511" : "#8a8e87"
                                }
                                Text {
                                    text: row.modelData.node_name
                                    color: "#141511"
                                    font.pixelSize: 14
                                    elide: Text.ElideRight
                                    Layout.fillWidth: true
                                }
                                Text {
                                    text: row.modelData.online ? "online" : "offline"
                                    color: "#8a8e87"
                                    font.pixelSize: 12
                                }
                            }
                        }
                    }
                }
            }
        }

        Item {
            Layout.fillWidth: true
            Layout.fillHeight: true

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
                    anchors.margins: 20
                    source: root.qrPath
                    fillMode: Image.PreserveAspectFit
                    visible: root.qrPath !== ""
                    cache: false
                }

                Column {
                    anchors.centerIn: parent
                    spacing: 8
                    visible: root.qrPath === ""
                    width: parent.width - 48

                    Text {
                        width: parent.width
                        text: root.busy ? "Creating…" : "QR appears here"
                        color: "#8a8e87"
                        font.pixelSize: 14
                        horizontalAlignment: Text.AlignHCenter
                    }
                    Text {
                        width: parent.width
                        visible: !root.busy
                        text: "Scan with Hath after you create a setup code."
                        color: "#b0b8a6"
                        font.pixelSize: 12
                        wrapMode: Text.WordWrap
                        horizontalAlignment: Text.AlignHCenter
                    }
                }
            }
        }
    }
}

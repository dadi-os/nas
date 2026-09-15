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
    property bool pairing: false
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

    function startPairing() {
        pairing = true
        status = ""
        copied = false
    }

    function finishPairing() {
        pairing = false
        busy = false
        bundle = ""
        qrPath = ""
        copied = false
        status = ""
        nameField.text = ""
        load()
    }

    Http { id: api }

    function load() {
        api.get(Tokens.nasBase + "/clients", function (code, body) {
            if (code !== 200) {
                if (!root.pairing)
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

    ColumnLayout {
        anchors.fill: parent
        spacing: 14
        visible: !root.pairing

        Text {
            text: "Devices"
            color: "#141511"
            font.pixelSize: 22
            font.weight: Font.DemiBold
            font.letterSpacing: -0.3
        }
        Text {
            text: "Hath clients on this mesh."
            color: "#8a8e87"
            font.pixelSize: 13
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        Rectangle {
            Layout.fillWidth: true
            Layout.fillHeight: true
            radius: 14
            color: Tokens.sageFill
            border.color: Tokens.sageStroke
            border.width: 1
            clip: true

            Column {
                anchors.centerIn: parent
                spacing: 14
                width: Math.min(320, parent.width - 48)
                visible: root.clients.length === 0

                Text {
                    width: parent.width
                    text: "No Hath on this mesh yet. Add one and scan the setup code from that device."
                    color: "#8a8e87"
                    font.pixelSize: 13
                    wrapMode: Text.WordWrap
                    horizontalAlignment: Text.AlignHCenter
                }
                DadiButton {
                    anchors.horizontalCenter: parent.horizontalCenter
                    text: "Add Hath"
                    onClicked: root.startPairing()
                }
            }

            DadiFlickable {
                anchors.fill: parent
                visible: root.clients.length > 0
                contentWidth: width
                contentHeight: roster.height

                Column {
                    id: roster
                    width: parent.width

                    Repeater {
                        model: root.clients
                        delegate: Item {
                            id: row
                            required property var modelData
                            required property int index
                            width: roster.width
                            height: 56

                            Rectangle {
                                anchors.left: parent.left
                                anchors.right: parent.right
                                anchors.leftMargin: 16
                                anchors.rightMargin: 16
                                height: 1
                                color: "#14151112"
                                visible: row.index > 0
                            }

                            RowLayout {
                                anchors.fill: parent
                                anchors.leftMargin: 20
                                anchors.rightMargin: 20
                                spacing: 12

                                Rectangle {
                                    width: 8
                                    height: 8
                                    radius: 4
                                    color: row.modelData.pending
                                           ? Tokens.sage
                                           : (row.modelData.online ? "#141511" : "#8a8e87")
                                }
                                Text {
                                    text: row.modelData.node_name
                                    color: "#141511"
                                    font.pixelSize: 15
                                    font.weight: Font.DemiBold
                                    elide: Text.ElideRight
                                    Layout.fillWidth: true
                                }
                                Text {
                                    text: row.modelData.pending ? "Waiting" : (row.modelData.online ? "Online" : "Offline")
                                    color: "#8a8e87"
                                    font.pixelSize: 13
                                }
                            }
                        }
                    }

                    Item {
                        width: roster.width
                        height: 56

                        DadiButton {
                            anchors.verticalCenter: parent.verticalCenter
                            anchors.left: parent.left
                            anchors.leftMargin: 20
                            text: "Add Hath"
                            onClicked: root.startPairing()
                        }
                    }
                }
            }
        }
    }

    Item {
        anchors.fill: parent
        visible: root.pairing

        DadiButton {
            kind: "ghost"
            text: "Done"
            anchors.top: parent.top
            anchors.right: parent.right
            onClicked: root.finishPairing()
        }

        ColumnLayout {
            anchors.centerIn: parent
            width: 320
            spacing: 12

            Text {
                text: "Add Hath"
                color: "#141511"
                font.pixelSize: 22
                font.weight: Font.DemiBold
                font.letterSpacing: -0.3
                Layout.alignment: Qt.AlignHCenter
            }
            Text {
                text: root.qrPath === ""
                      ? "Name it, then create a code to scan."
                      : "Scan with Hath. Single-use, about an hour."
                color: "#8a8e87"
                font.pixelSize: 13
                wrapMode: Text.WordWrap
                horizontalAlignment: Text.AlignHCenter
                Layout.fillWidth: true
            }

            FormRow {
                id: nameField
                Layout.fillWidth: true
                label: "Name"
                visible: root.qrPath === ""
            }

            DadiButton {
                Layout.fillWidth: true
                visible: root.qrPath === ""
                text: root.busy ? "Creating…" : "Create code"
                enabled: !root.busy && root.draftName.length > 0 && !root.nameTaken
                onClicked: root.mint()
            }

            Text {
                visible: root.nameTaken && root.qrPath === ""
                text: "“" + root.draftName + "” is already taken"
                color: "#c45c4a"
                font.pixelSize: 12
                wrapMode: Text.WordWrap
                Layout.fillWidth: true
                horizontalAlignment: Text.AlignHCenter
            }
            Text {
                text: root.status
                color: "#c45c4a"
                font.pixelSize: 12
                visible: root.status !== ""
                wrapMode: Text.WordWrap
                Layout.fillWidth: true
                horizontalAlignment: Text.AlignHCenter
            }

            Rectangle {
                Layout.alignment: Qt.AlignHCenter
                Layout.topMargin: 8
                Layout.preferredWidth: 300
                Layout.preferredHeight: 300
                visible: root.qrPath !== ""
                radius: 16
                color: "#ffffff"
                border.color: "#14151114"
                border.width: 1

                Image {
                    anchors.fill: parent
                    anchors.margins: 14
                    source: root.qrPath
                    fillMode: Image.PreserveAspectFit
                    cache: false
                }
            }

            DadiButton {
                Layout.fillWidth: true
                kind: "ghost"
                visible: root.bundle !== ""
                text: root.copied ? "Copied" : "Copy setup code"
                onClicked: root.copyBundle()
            }
        }
    }
}

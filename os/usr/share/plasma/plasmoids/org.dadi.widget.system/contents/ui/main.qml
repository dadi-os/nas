pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Layouts
import org.kde.plasma.plasmoid
import org.kde.plasma.core as PlasmaCore
import org.dadi.Desktop

PlasmoidItem {
    id: root
    preferredRepresentation: fullRepresentation
    Plasmoid.backgroundHints: PlasmaCore.Types.NoBackground

    fullRepresentation: CrestFrame {
        id: frame
        title: "System"
        Layout.minimumWidth: 280
        Layout.minimumHeight: 280
        Layout.preferredWidth: 420
        Layout.preferredHeight: 500

        property var status: ({})
        property var errors: []
        property string err: ""

        readonly property var serviceOrder: ["nas", "dimaag", "yaad", "dwar", "hath"]

        function services() {
            const raw = frame.status.services || []
            const byName = {}
            for (let i = 0; i < raw.length; i++)
                byName[raw[i].name] = raw[i].healthy
            byName["nas"] = true
            const rows = []
            const seen = {}
            for (let i = 0; i < serviceOrder.length; i++) {
                const name = serviceOrder[i]
                if (name === "nas" || byName[name] !== undefined) {
                    rows.push({ name: name, ok: !!byName[name] })
                    seen[name] = true
                }
            }
            for (let i = 0; i < raw.length; i++) {
                if (!seen[raw[i].name])
                    rows.push({ name: raw[i].name, ok: !!raw[i].healthy })
            }
            return rows
        }

        function pct(n) {
            if (n === undefined || n === null)
                return null
            return Math.max(0, Math.min(100, Math.round(n)))
        }

        function meters() {
            const s = frame.status
            const rows = []
            if (s.cpu)
                rows.push({ key: "cpu", label: "cpu", pct: pct(s.cpu.used_percent) })
            if (s.memory && s.memory.total_bytes > 0)
                rows.push({ key: "ram", label: "ram", pct: pct(s.memory.used_percent) })
            if (s.disk && s.disk.total_bytes > 0) {
                const used = (1 - s.disk.free_bytes / s.disk.total_bytes) * 100
                rows.push({ key: "disk", label: "disk", pct: pct(used) })
            }
            return rows
        }

        function latestTitle() {
            if (frame.errors.length === 0)
                return ""
            const e = frame.errors[0]
            return e.msg || e.raw || "error"
        }

        function refresh() {
            const st = new XMLHttpRequest()
            st.onreadystatechange = function () {
                if (st.readyState !== XMLHttpRequest.DONE)
                    return
                if (st.status !== 200) {
                    frame.err = "nas unreachable"
                    return
                }
                try {
                    frame.status = JSON.parse(st.responseText)
                    frame.err = ""
                } catch (e) {
                    frame.err = "bad status"
                }
            }
            st.open("GET", Tokens.nasBase + "/status")
            st.send()

            const to = new Date()
            const from = new Date(to.getTime() - 3600 * 1000)
            const lg = new XMLHttpRequest()
            lg.onreadystatechange = function () {
                if (lg.readyState !== XMLHttpRequest.DONE)
                    return
                if (lg.status !== 200)
                    return
                try {
                    const data = JSON.parse(lg.responseText)
                    frame.errors = data.entries || []
                } catch (e) {
                    frame.errors = []
                }
            }
            lg.open("GET", Tokens.nasBase + "/logs?level=error&limit=8&from="
                    + encodeURIComponent(from.toISOString())
                    + "&to=" + encodeURIComponent(to.toISOString()))
            lg.send()
        }

        Timer {
            interval: 2000
            running: true
            repeat: true
            triggeredOnStart: true
            onTriggered: frame.refresh()
        }

        ColumnLayout {
            anchors.fill: parent
            spacing: 10

            Text {
                visible: frame.err !== ""
                text: frame.err
                color: "#6e7568"
                font.pixelSize: 13
            }

            Flow {
                Layout.fillWidth: true
                spacing: 6
                visible: frame.err === "" && (frame.errors.length > 0)
                Repeater {
                    model: frame.services()
                    Rectangle {
                        required property var modelData
                        implicitHeight: 20
                        implicitWidth: chipLabel.width + 18
                        radius: 5
                        color: modelData.ok ? "transparent" : "#f7f0ed"
                        Row {
                            anchors.centerIn: parent
                            spacing: 5
                            Rectangle {
                                width: 6
                                height: 6
                                radius: 3
                                anchors.verticalCenter: parent.verticalCenter
                                color: modelData.ok ? "#8fa382" : "#9a5a4e"
                            }
                            Text {
                                id: chipLabel
                                text: modelData.name
                                color: modelData.ok ? "#b0b8a6" : "#9a5a4e"
                                font.pixelSize: 10
                            }
                        }
                    }
                }
            }

            ColumnLayout {
                Layout.fillWidth: true
                Layout.fillHeight: true
                spacing: 8
                visible: frame.err === ""

                ColumnLayout {
                    visible: frame.errors.length > 0
                    Layout.fillWidth: true
                    spacing: 6
                    Text {
                        text: frame.errors.length + " ERROR" + (frame.errors.length === 1 ? "" : "S") + " · 1H"
                        color: "#9a5a4e"
                        font.pixelSize: 10
                        font.letterSpacing: 1.5
                        font.weight: Font.Medium
                    }
                    Text {
                        text: frame.latestTitle()
                        color: "#2c302a"
                        font.pixelSize: 13
                        wrapMode: Text.WordWrap
                        maximumLineCount: 4
                        elide: Text.ElideRight
                        Layout.fillWidth: true
                    }
                    Text {
                        text: (frame.errors[0] && frame.errors[0].service) ? frame.errors[0].service : ""
                        color: "#6e7568"
                        font.pixelSize: 11
                        elide: Text.ElideRight
                        Layout.fillWidth: true
                    }
                }

                Repeater {
                    model: frame.errors.length === 0 ? frame.services() : []
                    RowLayout {
                        required property var modelData
                        Layout.fillWidth: true
                        Text {
                            text: modelData.name
                            color: "#2c302a"
                            font.pixelSize: 13
                            Layout.fillWidth: true
                        }
                        Text {
                            text: modelData.ok ? "reachable" : "unreachable"
                            color: modelData.ok ? "#6e7568" : "#9a5a4e"
                            font.pixelSize: 12
                        }
                    }
                }

                Item { Layout.fillHeight: true }
            }

            RowLayout {
                Layout.fillWidth: true
                spacing: 12
                visible: frame.err === ""
                Repeater {
                    model: frame.meters()
                    Text {
                        required property var modelData
                        text: modelData.label + " " + (modelData.pct === null ? "—" : modelData.pct + "%")
                        color: "#b0b8a6"
                        font.pixelSize: 10
                        font.family: "Noto Sans Mono"
                    }
                }
            }
        }
    }
}

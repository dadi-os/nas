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
        Layout.preferredWidth: 280
        Layout.preferredHeight: 280

        property var statusPayload: ({})
        property var errors: []

        readonly property var serviceOrder: ["nas", "dimaag", "yaad", "ghar", "dwar", "hath"]

        function services() {
            const raw = frame.statusPayload.services || []
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
            const s = frame.statusPayload
            const rows = []
            if (s.cpu)
                rows.push({ key: "cpu", label: "cpu", pct: pct(s.cpu.used_percent) })
            if (s.memory && s.memory.total_bytes > 0)
                rows.push({ key: "ram", label: "ram", pct: pct(s.memory.used_percent) })
            if (s.disk && s.disk.total_bytes > 0)
                rows.push({ key: "disk", label: "disk", pct: pct(s.disk.used_percent) })
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
                    frame.status = st.status === 0 ? "nas unreachable" : ("nas " + st.status)
                    return
                }
                try {
                    frame.statusPayload = JSON.parse(st.responseText)
                    frame.status = ""
                } catch (e) {
                    frame.status = "bad status"
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
            spacing: 14

            ColumnLayout {
                Layout.fillWidth: true
                spacing: 10
                visible: frame.status === ""

                Repeater {
                    model: frame.services()
                    RowLayout {
                        required property var modelData
                        Layout.fillWidth: true
                        spacing: 10
                        Text {
                            renderType: Text.QtRendering
                            text: modelData.name
                            color: "#141511"
                            font.pixelSize: 14
                            Layout.fillWidth: true
                        }
                        Text {
                            renderType: Text.QtRendering
                            text: modelData.ok ? "✓" : "✕"
                            color: modelData.ok ? "#141511" : "#c45c4a"
                            font.pixelSize: 16
                            font.weight: Font.DemiBold
                        }
                    }
                }

                ColumnLayout {
                    visible: frame.errors.length > 0
                    Layout.fillWidth: true
                    Layout.topMargin: 8
                    spacing: 6
                    Text {
                        renderType: Text.QtRendering
                        text: frame.errors.length + (frame.errors.length === 1 ? " error" : " errors") + " · 1h"
                        color: "#c45c4a"
                        font.pixelSize: 12
                    }
                    Text {
                        renderType: Text.QtRendering
                        text: frame.latestTitle()
                        color: "#141511"
                        font.pixelSize: 13
                        wrapMode: Text.WordWrap
                        maximumLineCount: 4
                        elide: Text.ElideRight
                        Layout.fillWidth: true
                    }
                    Text {
                        renderType: Text.QtRendering
                        text: (frame.errors[0] && frame.errors[0].service) ? frame.errors[0].service : ""
                        color: "#8a8e87"
                        font.pixelSize: 12
                        elide: Text.ElideRight
                        Layout.fillWidth: true
                    }
                }
            }

            Item { Layout.fillHeight: true }

            ColumnLayout {
                Layout.fillWidth: true
                spacing: 10
                visible: frame.status === ""
                Repeater {
                    model: frame.meters()
                    ColumnLayout {
                        required property var modelData
                        Layout.fillWidth: true
                        spacing: 4
                        RowLayout {
                            Layout.fillWidth: true
                            Text {
                                renderType: Text.QtRendering
                                text: modelData.label
                                color: "#8a8e87"
                                font.pixelSize: 11
                                font.letterSpacing: 1.2
                                font.capitalization: Font.AllUppercase
                            }
                            Item { Layout.fillWidth: true }
                            Text {
                                renderType: Text.QtRendering
                                text: modelData.pct === null ? "—" : modelData.pct + "%"
                                color: "#141511"
                                font.pixelSize: 12
                                font.family: "Noto Sans Mono"
                            }
                        }
                        Rectangle {
                            Layout.fillWidth: true
                            height: 5
                            radius: 3
                            color: "#14151114"
                            Rectangle {
                                width: parent.width * ((modelData.pct === null ? 0 : modelData.pct) / 100)
                                height: parent.height
                                radius: 3
                                color: "#141511"
                            }
                        }
                    }
                }
            }
        }
    }
}

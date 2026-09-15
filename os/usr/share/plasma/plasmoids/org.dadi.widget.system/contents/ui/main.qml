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
        property var clients: []
        property var errors: []
        property bool statusReachable: false

        readonly property var moduleOrder: ["dwar", "yaad", "dimaag", "ghar", "chaavi"]

        function erredNames() {
            const names = {}
            for (let i = 0; i < frame.errors.length; i++) {
                const name = String(frame.errors[i].service || "").trim()
                if (name !== "")
                    names[name] = true
            }
            return names
        }

        function modules() {
            const raw = frame.statusPayload.services || []
            const byName = {}
            for (let i = 0; i < raw.length; i++)
                byName[raw[i].name] = !!raw[i].healthy
            const erred = frame.erredNames()
            const rows = []
            const seen = {}
            for (let i = 0; i < moduleOrder.length; i++) {
                const name = moduleOrder[i]
                const healthy = frame.statusReachable && byName[name] === true
                rows.push({ name: name, ok: healthy && !erred[name] })
                seen[name] = true
            }
            for (let i = 0; i < raw.length; i++) {
                const name = raw[i].name
                if (seen[name])
                    continue
                const healthy = frame.statusReachable && !!raw[i].healthy
                rows.push({ name: name, ok: healthy && !erred[name] })
            }
            return rows
        }

        function meshClients() {
            const rows = []
            for (let i = 0; i < frame.clients.length; i++) {
                const c = frame.clients[i]
                const name = String(c.node_name || "").trim()
                if (name === "" || name.toLowerCase() === "os")
                    continue
                rows.push({
                    name: name,
                    pending: !!c.pending,
                    online: !!c.online
                })
            }
            return rows
        }

        function clientLabel(row) {
            if (row.pending)
                return "waiting"
            return row.online ? "online" : "offline"
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

        function refresh() {
            const st = new XMLHttpRequest()
            st.onreadystatechange = function () {
                if (st.readyState !== XMLHttpRequest.DONE)
                    return
                if (st.status !== 200) {
                    frame.statusReachable = false
                    frame.statusPayload = ({})
                    return
                }
                try {
                    frame.statusPayload = JSON.parse(st.responseText)
                    frame.statusReachable = true
                } catch (e) {
                    frame.statusReachable = false
                    frame.statusPayload = ({})
                }
            }
            st.open("GET", Tokens.nasBase + "/status")
            st.send()

            const cl = new XMLHttpRequest()
            cl.onreadystatechange = function () {
                if (cl.readyState !== XMLHttpRequest.DONE)
                    return
                if (cl.status !== 200) {
                    frame.clients = []
                    return
                }
                try {
                    const data = JSON.parse(cl.responseText)
                    frame.clients = Array.isArray(data.clients) ? data.clients : []
                } catch (e) {
                    frame.clients = []
                }
            }
            cl.open("GET", Tokens.nasBase + "/clients")
            cl.send()

            const to = new Date()
            const from = new Date(to.getTime() - 3600 * 1000)
            const lg = new XMLHttpRequest()
            lg.onreadystatechange = function () {
                if (lg.readyState !== XMLHttpRequest.DONE)
                    return
                if (lg.status !== 200) {
                    frame.errors = []
                    return
                }
                try {
                    const data = JSON.parse(lg.responseText)
                    frame.errors = Array.isArray(data.entries) ? data.entries : []
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
            interval: Tokens.widgetPollMs
            running: true
            repeat: true
            triggeredOnStart: true
            onTriggered: frame.refresh()
        }

        ColumnLayout {
            anchors.fill: parent
            spacing: 14

            DadiFlickable {
                Layout.fillWidth: true
                Layout.fillHeight: true
                contentWidth: width
                contentHeight: lists.height

                ColumnLayout {
                    id: lists
                    width: parent.width
                    spacing: 16

                    ColumnLayout {
                        Layout.fillWidth: true
                        spacing: 8
                        Text {
                            renderType: Text.QtRendering
                            text: "Modules"
                            color: "#8a8e87"
                            font.pixelSize: Tokens.typeSection
                            font.letterSpacing: 1.2
                            font.capitalization: Font.AllUppercase
                        }
                        Repeater {
                            model: {
                                frame.statusPayload
                                frame.errors
                                frame.statusReachable
                                return frame.modules()
                            }
                            RowLayout {
                                required property var modelData
                                Layout.fillWidth: true
                                spacing: 10
                                Text {
                                    renderType: Text.QtRendering
                                    text: modelData.name
                                    color: "#141511"
                                    font.pixelSize: Tokens.typeBody
                                    Layout.fillWidth: true
                                }
                                Text {
                                    renderType: Text.QtRendering
                                    text: modelData.ok ? "✓" : "✕"
                                    color: modelData.ok ? "#141511" : "#c45c4a"
                                    font.pixelSize: Tokens.typeMark
                                    font.weight: Font.DemiBold
                                }
                            }
                        }
                    }

                    ColumnLayout {
                        Layout.fillWidth: true
                        spacing: 8
                        Text {
                            renderType: Text.QtRendering
                            text: "Clients"
                            color: "#8a8e87"
                            font.pixelSize: Tokens.typeSection
                            font.letterSpacing: 1.2
                            font.capitalization: Font.AllUppercase
                        }
                        Text {
                            visible: frame.meshClients().length === 0
                            renderType: Text.QtRendering
                            text: "None on the mesh"
                            color: "#8a8e87"
                            font.pixelSize: Tokens.typeBody
                        }
                        Repeater {
                            model: frame.meshClients()
                            RowLayout {
                                required property var modelData
                                Layout.fillWidth: true
                                spacing: 10
                                Rectangle {
                                    width: 7
                                    height: 7
                                    radius: 4
                                    color: modelData.pending
                                           ? Tokens.sage
                                           : (modelData.online ? "#141511" : "#8a8e87")
                                }
                                Text {
                                    renderType: Text.QtRendering
                                    text: modelData.name
                                    color: "#141511"
                                    font.pixelSize: Tokens.typeBody
                                    elide: Text.ElideRight
                                    Layout.fillWidth: true
                                }
                                Text {
                                    renderType: Text.QtRendering
                                    text: frame.clientLabel(modelData)
                                    color: "#8a8e87"
                                    font.pixelSize: Tokens.typeMeta
                                }
                            }
                        }
                    }
                }
            }

            ColumnLayout {
                Layout.fillWidth: true
                spacing: 10
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
                                font.pixelSize: Tokens.typeSection
                                font.letterSpacing: 1.2
                                font.capitalization: Font.AllUppercase
                            }
                            Item { Layout.fillWidth: true }
                            Text {
                                renderType: Text.QtRendering
                                text: modelData.pct === null ? "—" : modelData.pct + "%"
                                color: "#141511"
                                font.pixelSize: Tokens.typeMeta
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

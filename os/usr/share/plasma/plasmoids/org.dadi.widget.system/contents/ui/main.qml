pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Layouts
import org.kde.plasma.plasmoid
import org.kde.plasma.core as PlasmaCore

PlasmoidItem {
    id: root

    preferredRepresentation: fullRepresentation
    Plasmoid.backgroundHints: PlasmaCore.Types.NoBackground

    fullRepresentation: CrestFrame {
        id: frame
        title: "System"
        Layout.minimumWidth: 280
        Layout.minimumHeight: 180
        Layout.preferredWidth: 340
        Layout.preferredHeight: 210

        property var status: ({})
        property string err: ""

        function fmtUptime(s) {
            s = Math.max(0, Math.floor(s || 0))
            const d = Math.floor(s / 86400)
            const h = Math.floor((s % 86400) / 3600)
            const m = Math.floor((s % 3600) / 60)
            if (d > 0)
                return d + "d " + h + "h"
            if (h > 0)
                return h + "h " + m + "m"
            return m + "m"
        }

        function refresh() {
            const xhr = new XMLHttpRequest()
            xhr.onreadystatechange = function () {
                if (xhr.readyState !== XMLHttpRequest.DONE)
                    return
                if (xhr.status !== 200) {
                    frame.err = "nas unreachable"
                    return
                }
                frame.err = ""
                try {
                    frame.status = JSON.parse(xhr.responseText)
                } catch (e) {
                    frame.err = "bad status"
                }
            }
            xhr.open("GET", "http://127.0.0.1:8092/status")
            xhr.send()
        }

        Timer {
            interval: 5000
            running: true
            repeat: true
            triggeredOnStart: true
            onTriggered: frame.refresh()
        }

        ColumnLayout {
            anchors.fill: parent
            spacing: 8

            RowLayout {
                Text {
                    text: frame.err === "" ? "ONLINE" : "OFFLINE"
                    color: frame.err === "" ? "#8fa382" : "#b0b8a6"
                    font.pixelSize: 11
                    font.letterSpacing: 2.5
                    font.weight: Font.Medium
                }
                Item { Layout.fillWidth: true }
                Text {
                    text: frame.err === "" ? frame.fmtUptime(frame.status.uptime_seconds) : "—"
                    color: "#b0b8a6"
                    font.pixelSize: 11
                }
            }

            Text {
                visible: frame.err !== ""
                text: frame.err
                color: "#6e7568"
                font.pixelSize: 12
            }

            Repeater {
                model: (frame.status.services || []).slice(0, 6)
                RowLayout {
                    required property var modelData
                    Layout.fillWidth: true
                    Text {
                        text: modelData.name
                        color: "#2c302a"
                        font.pixelSize: 12
                        Layout.fillWidth: true
                    }
                    Rectangle {
                        width: 7
                        height: 7
                        radius: 3.5
                        color: modelData.healthy ? "#8fa382" : "#b45046"
                    }
                }
            }

            Item { Layout.fillHeight: true }
        }
    }
}

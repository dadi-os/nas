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
        title: "Memory"
        Layout.minimumWidth: 260
        Layout.minimumHeight: 160
        Layout.preferredWidth: 300
        Layout.preferredHeight: 180

        property real usedPercent: 0
        property string label: "—"
        property string err: ""

        function fmtBytes(n) {
            if (!n || n <= 0)
                return "—"
            const gb = n / (1024 * 1024 * 1024)
            return gb.toFixed(1) + " GB"
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
                try {
                    const s = JSON.parse(xhr.responseText)
                    const mem = s.memory || {}
                    frame.usedPercent = mem.used_percent || 0
                    frame.label = frame.fmtBytes(mem.used_bytes) + " / " + frame.fmtBytes(mem.total_bytes)
                    frame.err = ""
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
            spacing: 12

            Text {
                text: frame.err !== "" ? frame.err : Math.round(frame.usedPercent) + "%"
                color: frame.err !== "" ? "#6e7568" : "#5c6b52"
                font.pixelSize: 36
                font.weight: Font.Medium
            }

            Rectangle {
                Layout.fillWidth: true
                height: 8
                radius: 4
                color: "#e4ebdc"
                Rectangle {
                    width: parent.width * Math.min(1, Math.max(0, frame.usedPercent / 100))
                    height: parent.height
                    radius: 4
                    color: "#8fa382"
                }
            }

            Text {
                text: frame.label
                color: "#6e7568"
                font.pixelSize: 12
            }

            Item { Layout.fillHeight: true }
        }
    }
}

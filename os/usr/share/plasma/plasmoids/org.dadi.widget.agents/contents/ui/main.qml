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
        title: "Agents"
        Layout.minimumWidth: 260
        Layout.minimumHeight: 140
        Layout.preferredWidth: 300
        Layout.preferredHeight: 170

        property int count: 0
        property string err: ""

        function refresh() {
            const xhr = new XMLHttpRequest()
            xhr.onreadystatechange = function () {
                if (xhr.readyState !== XMLHttpRequest.DONE)
                    return
                if (xhr.status !== 200) {
                    frame.err = "dimaag unreachable"
                    frame.count = 0
                    return
                }
                try {
                    const data = JSON.parse(xhr.responseText)
                    const list = Array.isArray(data) ? data : (data.agents || data.items || [])
                    frame.count = list.length
                    frame.err = ""
                } catch (e) {
                    frame.err = "bad agents payload"
                }
            }
            xhr.open("GET", "http://127.0.0.1:8083/agents")
            xhr.send()
        }

        Timer {
            interval: 8000
            running: true
            repeat: true
            triggeredOnStart: true
            onTriggered: frame.refresh()
        }

        ColumnLayout {
            anchors.fill: parent
            spacing: 8

            Text {
                text: frame.err !== "" ? "—" : String(frame.count)
                color: "#5c6b52"
                font.pixelSize: 42
                font.weight: Font.Medium
            }
            Text {
                text: frame.err !== "" ? frame.err : (frame.count === 1 ? "agent" : "agents")
                color: "#6e7568"
                font.pixelSize: 12
                font.letterSpacing: 1.5
            }
            Item { Layout.fillHeight: true }
        }
    }
}

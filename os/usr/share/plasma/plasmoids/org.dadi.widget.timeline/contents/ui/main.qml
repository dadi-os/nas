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
        title: "Timeline"
        Layout.minimumWidth: 280
        Layout.minimumHeight: 180
        Layout.preferredWidth: 340
        Layout.preferredHeight: 220

        property var plans: []
        property string err: ""

        function refresh() {
            const xhr = new XMLHttpRequest()
            xhr.onreadystatechange = function () {
                if (xhr.readyState !== XMLHttpRequest.DONE)
                    return
                if (xhr.status !== 200) {
                    frame.err = "yaad unreachable"
                    frame.plans = []
                    return
                }
                try {
                    const data = JSON.parse(xhr.responseText)
                    const nodes = data.nodes || []
                    frame.plans = nodes.slice(0, 3)
                    frame.err = ""
                } catch (e) {
                    frame.err = "bad query"
                }
            }
            xhr.open("POST", "http://127.0.0.1:8082/v1/query")
            xhr.setRequestHeader("Content-Type", "application/json")
            xhr.send(JSON.stringify({ kind: "plan", limit: 3, offset: 0 }))
        }

        Timer {
            interval: 15000
            running: true
            repeat: true
            triggeredOnStart: true
            onTriggered: frame.refresh()
        }

        ColumnLayout {
            anchors.fill: parent
            spacing: 8

            Text {
                visible: frame.err !== ""
                text: frame.err
                color: "#6e7568"
                font.pixelSize: 12
            }

            Text {
                visible: frame.err === "" && frame.plans.length === 0
                text: "No upcoming plans"
                color: "#a8af9f"
                font.pixelSize: 12
            }

            Repeater {
                model: frame.plans
                ColumnLayout {
                    required property var modelData
                    Layout.fillWidth: true
                    spacing: 2
                    Text {
                        text: modelData.name || modelData.title || modelData.id || "plan"
                        color: "#2c302a"
                        font.pixelSize: 13
                        elide: Text.ElideRight
                        Layout.fillWidth: true
                    }
                    Text {
                        text: modelData.status || modelData.kind || "plan"
                        color: "#a8af9f"
                        font.pixelSize: 11
                    }
                }
            }

            Item { Layout.fillHeight: true }
        }
    }
}

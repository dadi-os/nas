pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Layouts
import org.kde.plasma.plasmoid
import org.dadi.Desktop

PlasmoidItem {
    id: root
    preferredRepresentation: fullRepresentation

    property var services: []
    property int uptimeSeconds: 0
    property bool reachable: false

    /** uptimeText renders seconds as "3d 4h", "4h 12m", or "12m". */
    function uptimeText(seconds) {
        const days = Math.floor(seconds / 86400)
        const hours = Math.floor((seconds % 86400) / 3600)
        const minutes = Math.floor((seconds % 3600) / 60)
        if (days > 0)
            return days + "d " + hours + "h"
        if (hours > 0)
            return hours + "h " + minutes + "m"
        return minutes + "m"
    }

    function refresh() {
        Http.get(Tokens.nasBase + "/status", function (status, body) {
            if (status !== 200) {
                root.reachable = false
                return
            }
            const data = JSON.parse(body)
            root.services = data.services
            root.uptimeSeconds = data.uptime_seconds
            root.reachable = true
        })
    }

    Timer {
        interval: Tokens.widgetPollMs * 5
        running: true
        repeat: true
        triggeredOnStart: true
        onTriggered: root.refresh()
    }

    fullRepresentation: Item {
        Layout.minimumWidth: row.implicitWidth + 16
        Layout.preferredWidth: Layout.minimumWidth
        Layout.fillHeight: true

        RowLayout {
            id: row
            anchors.centerIn: parent
            spacing: 14

            Text {
                visible: !root.reachable
                renderType: Text.QtRendering
                text: "nas unreachable"
                color: Tokens.fail
                font.pixelSize: Tokens.typeMeta
            }

            Repeater {
                model: root.reachable ? root.services : []
                RowLayout {
                    required property var modelData
                    spacing: 6
                    Rectangle {
                        width: 8
                        height: 8
                        radius: 4
                        color: modelData.healthy ? Tokens.sage : Tokens.fail
                    }
                    Text {
                        renderType: Text.QtRendering
                        text: modelData.name
                        color: modelData.healthy ? Tokens.ink : Tokens.fail
                        font.pixelSize: Tokens.typeMeta
                    }
                }
            }

            Text {
                visible: root.reachable
                renderType: Text.QtRendering
                text: "up " + root.uptimeText(root.uptimeSeconds)
                color: Tokens.inkMuted
                font.pixelSize: Tokens.typeMeta
                font.family: "Noto Sans Mono"
            }
        }
    }
}

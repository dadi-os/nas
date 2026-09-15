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
        title: "Memory"
        Layout.minimumWidth: 480
        Layout.minimumHeight: 180
        Layout.preferredWidth: 480
        Layout.preferredHeight: 180

        property int people: -1
        property int memories: -1
        property int places: -1
        property int plans: -1
        property int pending: 0

        function fail(xhr) {
            if (xhr.status === 0) {
                frame.status = "yaad unreachable"
                return
            }
            let type = ""
            try {
                const data = JSON.parse(xhr.responseText)
                if (data.error && data.error.type)
                    type = data.error.type
            } catch (e) {
                frame.status = "yaad " + xhr.status
                return
            }
            frame.status = type !== "" ? type : ("yaad " + xhr.status)
        }

        function countKind(kind, assign) {
            const xhr = new XMLHttpRequest()
            xhr.onreadystatechange = function () {
                if (xhr.readyState !== XMLHttpRequest.DONE)
                    return
                frame.pending = Math.max(0, frame.pending - 1)
                if (xhr.status !== 200) {
                    frame.fail(xhr)
                    return
                }
                try {
                    const data = JSON.parse(xhr.responseText)
                    assign((data.nodes || []).length)
                } catch (e) {
                    frame.status = "bad query"
                }
            }
            xhr.open("POST", Tokens.yaadBase + "/query")
            xhr.setRequestHeader("Content-Type", "application/json")
            xhr.send(JSON.stringify({ kind: kind, limit: 200, offset: 0 }))
        }

        function refresh() {
            frame.pending = 4
            frame.status = ""
            countKind("person", function (n) { frame.people = n })
            countKind("memory", function (n) { frame.memories = n })
            countKind("place", function (n) { frame.places = n })
            countKind("plan", function (n) { frame.plans = n })
        }

        function fmt(n) {
            if (n < 0)
                return "—"
            if (n >= 200)
                return "200+"
            return String(n)
        }

        Timer {
            interval: 4000
            running: true
            repeat: true
            triggeredOnStart: true
            onTriggered: frame.refresh()
        }

        RowLayout {
            anchors.fill: parent
            spacing: 0

            Repeater {
                model: [
                    { value: frame.fmt(frame.people), label: "PEOPLE" },
                    { value: frame.fmt(frame.memories), label: "MEMORIES" },
                    { value: frame.fmt(frame.places), label: "PLACES" },
                    { value: frame.fmt(frame.plans), label: "PLANS" }
                ]
                Item {
                    required property var modelData
                    required property int index
                    Layout.fillWidth: true
                    Layout.fillHeight: true

                    Rectangle {
                        visible: index > 0
                        anchors.left: parent.left
                        anchors.verticalCenter: parent.verticalCenter
                        width: 1
                        height: parent.height * 0.5
                        color: "#14151114"
                        opacity: 0.55
                    }

                    Column {
                        anchors.centerIn: parent
                        spacing: 10

                        Text {
                            renderType: Text.QtRendering
                            anchors.horizontalCenter: parent.horizontalCenter
                            text: modelData.value
                            color: "#141511"
                            font.pixelSize: 32
                            font.weight: Font.Medium
                            font.letterSpacing: -0.8
                        }
                        Text {
                            renderType: Text.QtRendering
                            anchors.horizontalCenter: parent.horizontalCenter
                            text: modelData.label
                            color: "#8a8e87"
                            font.pixelSize: 10
                            font.letterSpacing: 2.2
                            font.weight: Font.Medium
                        }
                    }
                }
            }
        }
    }
}

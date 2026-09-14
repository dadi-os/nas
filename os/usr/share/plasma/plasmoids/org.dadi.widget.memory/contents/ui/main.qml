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
        Layout.preferredWidth: 860
        Layout.preferredHeight: 230

        property int people: -1
        property int memories: -1
        property int places: -1
        property int plans: -1
        property string err: ""
        property int pending: 0

        function countKind(kind, assign) {
            const xhr = new XMLHttpRequest()
            xhr.onreadystatechange = function () {
                if (xhr.readyState !== XMLHttpRequest.DONE)
                    return
                frame.pending = Math.max(0, frame.pending - 1)
                if (xhr.status !== 200) {
                    frame.err = "yaad unreachable"
                    return
                }
                try {
                    const data = JSON.parse(xhr.responseText)
                    const n = (data.nodes || []).length
                    assign(n)
                    if (frame.pending === 0 && frame.err === "yaad unreachable")
                        return
                    if (xhr.status === 200 && frame.pending === 0)
                        frame.err = ""
                } catch (e) {
                    frame.err = "bad query"
                }
            }
            xhr.open("POST", Tokens.yaadBase + "/v1/query")
            xhr.setRequestHeader("Content-Type", "application/json")
            xhr.send(JSON.stringify({ kind: kind, limit: 500, offset: 0 }))
        }

        function refresh() {
            frame.pending = 4
            frame.err = ""
            countKind("person", function (n) { frame.people = n })
            countKind("memory", function (n) { frame.memories = n })
            countKind("place", function (n) { frame.places = n })
            countKind("plan", function (n) { frame.plans = n })
        }

        function fmt(n) {
            if (n < 0)
                return "—"
            if (n >= 500)
                return "500+"
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
                        height: parent.height * 0.55
                        color: "#b9c9ab"
                        opacity: 0.7
                    }

                    Column {
                        anchors.centerIn: parent
                        spacing: 8

                        Text {
                            anchors.horizontalCenter: parent.horizontalCenter
                            text: frame.err !== "" ? "—" : modelData.value
                            color: "#2c302a"
                            font.pixelSize: 26
                            font.weight: Font.Medium
                        }
                        Text {
                            anchors.horizontalCenter: parent.horizontalCenter
                            text: modelData.label
                            color: "#5c6b52"
                            font.pixelSize: 10
                            font.letterSpacing: 2
                            font.weight: Font.Medium
                        }
                    }
                }
            }
        }

        Text {
            anchors.centerIn: parent
            visible: frame.err !== ""
            text: frame.err
            color: "#6e7568"
            font.pixelSize: 13
            z: 2
        }
    }
}

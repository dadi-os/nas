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
        title: "Timeline"
        Layout.minimumWidth: 480
        Layout.minimumHeight: 280
        Layout.preferredWidth: 860
        Layout.preferredHeight: 500

        property var days: []
        property var plans: []
        property string weekTitle: ""
        property string err: ""

        function startOfDay(d) {
            const x = new Date(d)
            x.setHours(0, 0, 0, 0)
            return x
        }

        function startOfWeek(d) {
            const day = startOfDay(d)
            const weekday = (day.getDay() + 6) % 7
            day.setDate(day.getDate() - weekday)
            return day
        }

        function addDays(d, n) {
            const x = new Date(d)
            x.setDate(x.getDate() + n)
            return x
        }

        function sameDay(a, b) {
            return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate()
        }

        function monthShort(d) {
            return ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"][d.getMonth()]
        }

        function formatWeekTitle(start) {
            const end = addDays(start, 6)
            if (start.getMonth() === end.getMonth())
                return monthShort(start) + " " + start.getDate() + "–" + end.getDate() + ", " + start.getFullYear()
            return monthShort(start) + " " + start.getDate() + " – " + monthShort(end) + " " + end.getDate() + ", " + end.getFullYear()
        }

        function planStatus(plan) {
            if (plan.detail && plan.detail.status)
                return plan.detail.status
            return "confirmed"
        }

        function plansForDay(day) {
            const out = []
            for (let i = 0; i < frame.plans.length; i++) {
                const plan = frame.plans[i]
                if (planStatus(plan) === "idea")
                    continue
                if (!plan.occurred_at)
                    continue
                const start = new Date(plan.occurred_at)
                const end = plan.detail && plan.detail.end_at ? new Date(plan.detail.end_at) : start
                const dayStart = startOfDay(day).getTime()
                const dayEnd = addDays(startOfDay(day), 1).getTime() - 1
                if (start.getTime() <= dayEnd && end.getTime() >= dayStart)
                    out.push(plan)
            }
            return out
        }

        function chipColor(status) {
            if (status === "tentative")
                return "#8fa38222"
            return "#8fa38228"
        }

        function chipText(status) {
            if (status === "tentative")
                return "#7e9270"
            return "#2c302a"
        }

        function refresh() {
            const start = startOfWeek(new Date())
            const end = addDays(start, 6)
            end.setHours(23, 59, 59, 999)
            const week = []
            for (let i = 0; i < 7; i++)
                week.push(addDays(start, i))
            frame.days = week
            frame.weekTitle = formatWeekTitle(start)

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
                    frame.plans = data.nodes || []
                    frame.err = ""
                } catch (e) {
                    frame.err = "bad query"
                }
            }
            xhr.open("POST", Tokens.yaadBase + "/v1/query")
            xhr.setRequestHeader("Content-Type", "application/json")
            xhr.send(JSON.stringify({
                kind: "plan",
                occurred_from: start.toISOString(),
                occurred_to: end.toISOString(),
                limit: 200
            }))
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
            spacing: 10

            Text {
                text: frame.weekTitle
                color: "#b0b8a6"
                font.pixelSize: 11
            }

            Text {
                visible: frame.err !== ""
                text: frame.err
                color: "#6e7568"
                font.pixelSize: 13
            }

            RowLayout {
                Layout.fillWidth: true
                Layout.fillHeight: true
                spacing: 6

                Repeater {
                    model: frame.days
                    Rectangle {
                        required property var modelData
                        Layout.fillWidth: true
                        Layout.fillHeight: true
                        radius: 8
                        color: frame.sameDay(modelData, frame.startOfDay(new Date())) ? "#8fa38228" : "transparent"

                        ColumnLayout {
                            anchors.fill: parent
                            anchors.margins: 6
                            spacing: 4

                            Text {
                                text: modelData.getDate()
                                color: frame.sameDay(modelData, frame.startOfDay(new Date())) ? "#5c6b52" : "#b0b8a6"
                                font.pixelSize: 12
                                font.weight: frame.sameDay(modelData, frame.startOfDay(new Date())) ? Font.Medium : Font.Normal
                            }

                            Repeater {
                                model: frame.plansForDay(modelData).slice(0, 4)
                                Rectangle {
                                    required property var modelData
                                    Layout.fillWidth: true
                                    implicitHeight: chipText.height + 8
                                    radius: 5
                                    color: frame.chipColor(frame.planStatus(modelData))

                                    Text {
                                        id: chipText
                                        anchors.left: parent.left
                                        anchors.right: parent.right
                                        anchors.verticalCenter: parent.verticalCenter
                                        anchors.margins: 5
                                        text: modelData.title || modelData.name || "plan"
                                        color: frame.chipText(frame.planStatus(modelData))
                                        font.pixelSize: 9
                                        elide: Text.ElideRight
                                    }
                                }
                            }

                            Text {
                                visible: frame.plansForDay(modelData).length > 4
                                text: "+" + (frame.plansForDay(modelData).length - 4)
                                color: "#6e7568"
                                font.pixelSize: 9
                            }

                            Item { Layout.fillHeight: true }
                        }
                    }
                }
            }
        }
    }
}

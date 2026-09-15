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
        Layout.preferredWidth: 480
        Layout.preferredHeight: 280

        property var days: []
        property var plans: []
        property string weekTitle: ""

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

        function weekdayLetter(d) {
            return ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"][(d.getDay() + 6) % 7]
        }

        function formatWeekTitle(start) {
            const end = addDays(start, 6)
            if (start.getMonth() === end.getMonth())
                return monthShort(start) + " " + start.getDate() + "–" + end.getDate()
            return monthShort(start) + " " + start.getDate() + " – " + monthShort(end) + " " + end.getDate()
        }

        function planStatus(plan) {
            if (plan.detail && plan.detail.status)
                return plan.detail.status
            return "confirmed"
        }

        function formatTime(iso) {
            const d = new Date(iso)
            let h = d.getHours()
            const m = d.getMinutes()
            const ap = h >= 12 ? "PM" : "AM"
            h = h % 12
            if (h === 0)
                h = 12
            if (m === 0)
                return String(h) + " " + ap
            return h + ":" + (m < 10 ? "0" : "") + m + " " + ap
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
                    frame.fail(xhr)
                    frame.kicker = ""
                    frame.plans = []
                    return
                }
                try {
                    const data = JSON.parse(xhr.responseText)
                    frame.plans = data.nodes || []
                    frame.status = ""
                    const n = frame.plans.length
                    frame.kicker = n === 0 ? frame.weekTitle : (n + (n === 1 ? " plan" : " plans"))
                } catch (e) {
                    frame.status = "bad query"
                    frame.kicker = ""
                }
            }
            xhr.open("POST", Tokens.yaadBase + "/query")
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

        RowLayout {
            anchors.fill: parent
            spacing: 0

            Repeater {
                model: frame.days
                Item {
                    required property var modelData
                    required property int index
                    readonly property bool today: frame.sameDay(modelData, frame.startOfDay(new Date()))
                    Layout.fillWidth: true
                    Layout.fillHeight: true

                    Rectangle {
                        visible: index > 0
                        anchors.left: parent.left
                        anchors.top: parent.top
                        anchors.bottom: parent.bottom
                        width: 1
                        color: "#141511"
                        opacity: 0.06
                    }

                    ColumnLayout {
                        anchors.fill: parent
                        anchors.leftMargin: 12
                        anchors.rightMargin: 10
                        anchors.topMargin: 4
                        anchors.bottomMargin: 8
                        spacing: 10

                        Text {
                            text: frame.weekdayLetter(modelData)
                            color: today ? "#141511" : "#8a8e87"
                            font.pixelSize: 11
                            font.weight: Font.Medium
                        }

                        Item {
                            width: 32
                            height: 32

                            Rectangle {
                                visible: today
                                anchors.fill: parent
                                radius: width / 2
                                color: "#141511"
                            }

                            Text {
                                anchors.centerIn: parent
                                text: modelData.getDate()
                                color: today ? "#ffffff" : "#141511"
                                font.pixelSize: 16
                                font.weight: Font.DemiBold
                            }
                        }

                        Repeater {
                            model: frame.plansForDay(modelData).slice(0, 5)
                            Column {
                                required property var modelData
                                Layout.fillWidth: true
                                spacing: 2

                                Text {
                                    text: frame.formatTime(modelData.occurred_at)
                                    color: "#8a8e87"
                                    font.pixelSize: 11
                                }
                                Text {
                                    width: parent.width
                                    text: modelData.title || "plan"
                                    color: "#141511"
                                    font.pixelSize: 13
                                    font.weight: Font.Medium
                                    elide: Text.ElideRight
                                    wrapMode: Text.NoWrap
                                }
                            }
                        }

                        Item { Layout.fillHeight: true }
                    }
                }
            }
        }
    }
}

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
        title: "Ghar"
        Layout.minimumWidth: 280
        Layout.minimumHeight: 280
        Layout.preferredWidth: 280
        Layout.preferredHeight: 280

        property var devices: []
        property var rooms: []

        function roomRows() {
            const byId = {}
            const order = []
            for (let i = 0; i < frame.devices.length; i++) {
                const dev = frame.devices[i]
                const roomId = (dev.room && dev.room.id) ? dev.room.id : "unassigned"
                if (!byId[roomId]) {
                    byId[roomId] = {
                        id: roomId,
                        name: (dev.room && dev.room.name) ? dev.room.name : "unassigned",
                        devices: []
                    }
                    order.push(roomId)
                }
                byId[roomId].devices.push(dev)
            }
            const rows = []
            for (let i = 0; i < order.length; i++)
                rows.push(byId[order[i]])
            return rows
        }

        function isOn(dev) {
            return !!(dev.state && dev.state.on && dev.state.on.value === true)
        }

        function canSwitch(dev) {
            const caps = dev.capabilities || []
            for (let i = 0; i < caps.length; i++) {
                if (caps[i].capability === "switchable")
                    return true
            }
            return false
        }

        function fail(xhr, service) {
            if (xhr.status === 0) {
                frame.status = service + " unreachable"
                return
            }
            let type = ""
            try {
                const data = JSON.parse(xhr.responseText)
                if (data.error && data.error.type)
                    type = data.error.type
            } catch (e) {
                frame.status = service + " " + xhr.status
                return
            }
            frame.status = type !== "" ? type : (service + " " + xhr.status)
        }

        function refresh() {
            const roomsReq = new XMLHttpRequest()
            roomsReq.onreadystatechange = function () {
                if (roomsReq.readyState !== XMLHttpRequest.DONE)
                    return
                if (roomsReq.status !== 200) {
                    frame.fail(roomsReq, "ghar")
                    return
                }
                try {
                    const data = JSON.parse(roomsReq.responseText)
                    frame.rooms = data.rooms || []
                } catch (e) {
                    frame.status = "bad rooms"
                }
            }
            roomsReq.open("GET", Tokens.gharBase + "/rooms")
            roomsReq.send()

            const xhr = new XMLHttpRequest()
            xhr.onreadystatechange = function () {
                if (xhr.readyState !== XMLHttpRequest.DONE)
                    return
                if (xhr.status !== 200) {
                    frame.fail(xhr, "ghar")
                    frame.kicker = ""
                    frame.devices = []
                    return
                }
                try {
                    const data = JSON.parse(xhr.responseText)
                    frame.devices = data.devices || []
                    frame.status = ""
                    const n = frame.devices.length
                    frame.kicker = n === 0 ? "" : (n + (n === 1 ? " device" : " devices"))
                } catch (e) {
                    frame.status = "bad devices"
                    frame.kicker = ""
                }
            }
            xhr.open("GET", Tokens.gharBase + "/devices")
            xhr.send()
        }

        function toggle(dev) {
            const xhr = new XMLHttpRequest()
            xhr.onreadystatechange = function () {
                if (xhr.readyState !== XMLHttpRequest.DONE)
                    return
                if (xhr.status !== 200) {
                    frame.fail(xhr, "ghar")
                    return
                }
                frame.refresh()
            }
            xhr.open("POST", Tokens.gharBase + "/devices/" + dev.id + "/command")
            xhr.setRequestHeader("Content-Type", "application/json")
            xhr.send(JSON.stringify({
                capability: "switchable",
                params: { state: "toggle" },
                cause: "user"
            }))
        }

        Timer {
            interval: Tokens.widgetPollMs
            running: true
            repeat: true
            triggeredOnStart: true
            onTriggered: frame.refresh()
        }

        Item {
            anchors.fill: parent
            visible: frame.status === "" && frame.devices.length === 0

            Text {
                renderType: Text.QtRendering
                anchors.centerIn: parent
                text: "No devices"
                color: "#8a8e87"
                font.pixelSize: Tokens.typeBody
            }
        }

        DadiFlickable {
            anchors.fill: parent
            visible: frame.devices.length > 0
            contentWidth: width
            contentHeight: listCol.height

            Column {
                id: listCol
                width: parent.width
                spacing: 16

                Repeater {
                    model: frame.roomRows()
                    Column {
                        required property var modelData
                        width: listCol.width
                        spacing: 8

                        Text {
                            renderType: Text.QtRendering
                            visible: frame.roomRows().length > 1
                            text: modelData.name
                            color: "#8a8e87"
                            font.pixelSize: Tokens.typeSection
                            font.weight: Font.Medium
                        }

                        Repeater {
                            model: modelData.devices
                            Item {
                                required property var modelData
                                width: listCol.width
                                implicitHeight: 44

                                RowLayout {
                                    anchors.fill: parent
                                    spacing: 10

                                    Rectangle {
                                        width: 7
                                        height: 7
                                        radius: 4
                                        color: modelData.online ? "#141511" : "#8a8e87"
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
                                        visible: frame.canSwitch(modelData)
                                        text: frame.isOn(modelData) ? "on" : "off"
                                        color: frame.isOn(modelData) ? "#141511" : "#8a8e87"
                                        font.pixelSize: Tokens.typeMeta
                                    }
                                }

                                MouseArea {
                                    anchors.fill: parent
                                    enabled: frame.canSwitch(modelData)
                                    cursorShape: enabled ? Qt.PointingHandCursor : Qt.ArrowCursor
                                    onClicked: frame.toggle(modelData)
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

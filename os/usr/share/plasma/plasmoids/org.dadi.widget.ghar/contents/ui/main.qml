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
        property var pendingIds: ({})
        property int pendingCount: 0

        function roomTitle(name) {
            return name === "unassigned" ? "Unplaced" : name
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

        function isPending(id) {
            return !!frame.pendingIds[id]
        }

        function roomPanels() {
            const byId = {}
            const named = []
            for (let i = 0; i < frame.rooms.length; i++) {
                const room = frame.rooms[i]
                if (!room || room.name === "unassigned")
                    continue
                if (byId[room.id])
                    continue
                byId[room.id] = { id: room.id, name: room.name, devices: [] }
                named.push(byId[room.id])
            }
            let unplaced = null
            for (let i = 0; i < frame.devices.length; i++) {
                const dev = frame.devices[i]
                const room = dev.room || {}
                const roomId = room.id || "unassigned"
                const roomName = room.name || "unassigned"
                if (roomName === "unassigned") {
                    if (!unplaced) {
                        unplaced = { id: roomId, name: "unassigned", devices: [] }
                    }
                    unplaced.devices.push(dev)
                    continue
                }
                if (!byId[roomId]) {
                    byId[roomId] = { id: roomId, name: roomName, devices: [] }
                    named.push(byId[roomId])
                }
                byId[roomId].devices.push(dev)
            }
            const panels = []
            for (let i = 0; i < named.length; i++) {
                if (named[i].devices.length > 0)
                    panels.push(named[i])
            }
            if (unplaced && unplaced.devices.length > 0)
                panels.push(unplaced)
            return panels
        }

        function roomLit(panel) {
            const list = panel.devices || []
            for (let i = 0; i < list.length; i++) {
                const dev = list[i]
                if (dev.online && frame.canSwitch(dev) && frame.isOn(dev))
                    return true
            }
            return false
        }

        function roomPending(panel) {
            const list = panel.devices || []
            for (let i = 0; i < list.length; i++) {
                if (frame.isPending(list[i].id))
                    return true
            }
            return false
        }

        function roomCanToggle(panel) {
            const list = panel.devices || []
            for (let i = 0; i < list.length; i++) {
                const dev = list[i]
                if (dev.online && frame.canSwitch(dev))
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
            if (frame.pendingCount > 0)
                return

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

        function markPending(ids, on) {
            const next = Object.assign({}, frame.pendingIds)
            for (let i = 0; i < ids.length; i++) {
                if (on)
                    next[ids[i]] = true
                else
                    delete next[ids[i]]
            }
            frame.pendingIds = next
            frame.pendingCount = Object.keys(next).length
        }

        function postToggle(id, onDone) {
            const xhr = new XMLHttpRequest()
            xhr.onreadystatechange = function () {
                if (xhr.readyState !== XMLHttpRequest.DONE)
                    return
                if (xhr.status !== 200)
                    frame.fail(xhr, "ghar")
                onDone()
            }
            xhr.open("POST", Tokens.gharBase + "/devices/" + id + "/command")
            xhr.setRequestHeader("Content-Type", "application/json")
            xhr.send(JSON.stringify({
                capability: "switchable",
                params: { state: "toggle" },
                cause: "user"
            }))
        }

        function toggleRoom(panel) {
            if (frame.roomPending(panel))
                return
            const lights = []
            const list = panel.devices || []
            for (let i = 0; i < list.length; i++) {
                const dev = list[i]
                if (dev.online && frame.canSwitch(dev))
                    lights.push(dev)
            }
            if (lights.length === 0)
                return
            let anyOn = false
            for (let i = 0; i < lights.length; i++) {
                if (frame.isOn(lights[i])) {
                    anyOn = true
                    break
                }
            }
            const targets = []
            for (let i = 0; i < lights.length; i++) {
                if (!anyOn || frame.isOn(lights[i]))
                    targets.push(lights[i].id)
            }
            if (targets.length === 0)
                return
            frame.markPending(targets, true)
            let remaining = targets.length
            for (let i = 0; i < targets.length; i++) {
                frame.postToggle(targets[i], function () {
                    remaining -= 1
                    if (remaining > 0)
                        return
                    frame.markPending(targets, false)
                    frame.refresh()
                })
            }
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

        Item {
            anchors.fill: parent
            visible: frame.status === "" && frame.devices.length > 0 && frame.roomPanels().length === 0

            Text {
                renderType: Text.QtRendering
                anchors.centerIn: parent
                text: "Unplaced"
                color: "#8a8e87"
                font.pixelSize: Tokens.typeBody
            }
        }

        GridLayout {
            anchors.fill: parent
            columns: 2
            rowSpacing: 8
            columnSpacing: 8
            visible: frame.status === "" && frame.roomPanels().length > 0

            Repeater {
                model: frame.roomPanels()
                Rectangle {
                    required property var modelData
                    Layout.fillWidth: true
                    Layout.fillHeight: true
                    radius: Tokens.radiusControl
                    color: {
                        if (frame.roomPending(modelData))
                            return "#14151114"
                        if (frame.roomLit(modelData))
                            return Tokens.sageFaint
                        return "#fafaf766"
                    }
                    border.width: 1
                    border.color: {
                        if (frame.roomPending(modelData))
                            return Tokens.rule
                        if (frame.roomLit(modelData))
                            return Tokens.sage
                        return Tokens.sageLine
                    }

                    RowLayout {
                        anchors.fill: parent
                        anchors.margins: 12
                        spacing: 8

                        Text {
                            renderType: Text.QtRendering
                            text: frame.roomTitle(modelData.name)
                            color: frame.roomPending(modelData)
                                   ? Tokens.inkMuted
                                   : (frame.roomLit(modelData) ? Tokens.sageDeep : Tokens.inkMuted)
                            font.pixelSize: Tokens.typeSection
                            font.weight: Font.Medium
                            font.capitalization: Font.AllUppercase
                            font.letterSpacing: 1.2
                            elide: Text.ElideRight
                            Layout.fillWidth: true
                        }

                        Rectangle {
                            width: 7
                            height: 7
                            radius: 4
                            color: frame.roomPending(modelData)
                                   ? Tokens.inkMuted
                                   : (frame.roomLit(modelData) ? Tokens.sage : Tokens.inkMuted)
                            opacity: frame.roomPending(modelData) ? breathOpacity : 1
                            property real breathOpacity: 1

                            SequentialAnimation on breathOpacity {
                                running: frame.roomPending(modelData)
                                loops: Animation.Infinite
                                NumberAnimation { from: 0.35; to: 1; duration: 900; easing.type: Easing.InOutSine }
                                NumberAnimation { from: 1; to: 0.35; duration: 900; easing.type: Easing.InOutSine }
                            }
                        }
                    }

                    MouseArea {
                        anchors.fill: parent
                        enabled: !frame.roomPending(modelData) && frame.roomCanToggle(modelData)
                        cursorShape: enabled ? Qt.PointingHandCursor : Qt.ArrowCursor
                        onClicked: frame.toggleRoom(modelData)
                    }
                }
            }
        }

        Text {
            anchors.centerIn: parent
            visible: frame.status !== ""
            renderType: Text.QtRendering
            text: frame.status
            color: Tokens.fail
            font.pixelSize: Tokens.typeBody
        }
    }
}

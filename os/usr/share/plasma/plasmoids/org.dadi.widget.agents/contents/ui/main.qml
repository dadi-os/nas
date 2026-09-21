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
        title: "Agents"
        Layout.minimumWidth: 480
        Layout.minimumHeight: 180
        Layout.preferredWidth: 480
        Layout.preferredHeight: 180

        property var nodes: []
        property var links: []
        property real contentMinX: 0
        property real contentMinY: 0
        property real contentMaxX: 0
        property real contentMaxY: 0

        function visualOf(agent) {
            if (!agent.active)
                return "dormant"
            const lanes = agent.running || {}
            if (lanes.reasoning && lanes.conversation)
                return "both"
            if (lanes.reasoning)
                return "reasoning"
            if (lanes.conversation)
                return "conversation"
            return "idle"
        }

        function captionOf(visual) {
            if (visual === "dormant")
                return "dormant"
            if (visual === "reasoning")
                return "reasoning"
            if (visual === "conversation")
                return "talking"
            if (visual === "both")
                return "both"
            return "idle"
        }

        function isLive(visual) {
            return visual === "reasoning" || visual === "conversation" || visual === "both"
        }

        function buildForest(agents) {
            if (!agents || agents.length === 0)
                return []
            const byId = {}
            for (let i = 0; i < agents.length; i++)
                byId[agents[i].id] = agents[i]
            const childrenOf = {}
            const roots = []
            for (let i = 0; i < agents.length; i++) {
                const agent = agents[i]
                const parentId = agent.parent_agent_id
                if (parentId === null || parentId === undefined || !byId[parentId]) {
                    roots.push(agent)
                    continue
                }
                if (!childrenOf[parentId])
                    childrenOf[parentId] = []
                childrenOf[parentId].push(agent)
            }
            function toNode(agent) {
                const kids = childrenOf[agent.id] || []
                const visual = frame.visualOf(agent)
                const node = {
                    id: agent.id,
                    name: agent.name,
                    visual: visual,
                    caption: frame.captionOf(visual),
                    children: []
                }
                for (let i = 0; i < kids.length; i++)
                    node.children.push(toNode(kids[i]))
                return node
            }
            const forest = []
            for (let i = 0; i < roots.length; i++)
                forest.push(toNode(roots[i]))
            return forest
        }

        function layoutCircle(forest, ring, step) {
            const nodes = []
            const links = []
            const count = forest.length
            if (count === 0)
                return { nodes: nodes, links: links }
            const sector = (Math.PI * 2) / count

            function place(data, angle, radius, depth, parentPos) {
                const x = Math.cos(angle) * radius
                const y = Math.sin(angle) * radius
                if (parentPos) {
                    links.push({
                        x1: parentPos.x,
                        y1: parentPos.y,
                        x2: x,
                        y2: y
                    })
                }
                nodes.push({
                    id: data.id,
                    name: data.name,
                    visual: data.visual,
                    caption: data.caption,
                    x: x,
                    y: y
                })
                const kids = data.children || []
                const fan = Math.min(sector * 0.62, 0.7)
                for (let i = 0; i < kids.length; i++) {
                    const spread = kids.length === 1
                            ? 0
                            : (i - (kids.length - 1) / 2) * (fan / kids.length)
                    place(kids[i], angle + spread, radius + step, depth + 1, { x: x, y: y })
                }
            }

            for (let i = 0; i < forest.length; i++) {
                const angle = -Math.PI / 2 + i * sector
                place(forest[i], angle, ring, 0, null)
            }
            return { nodes: nodes, links: links }
        }

        function refresh() {
            const xhr = new XMLHttpRequest()
            xhr.onreadystatechange = function () {
                if (xhr.readyState !== XMLHttpRequest.DONE)
                    return
                if (xhr.status !== 200) {
                    frame.status = xhr.status === 0 ? "dimaag unreachable" : ("dimaag " + xhr.status)
                    frame.kicker = ""
                    frame.nodes = []
                    frame.links = []
                    return
                }
                try {
                    const data = JSON.parse(xhr.responseText)
                    const list = Array.isArray(data) ? data : (data.agents || [])
                    if (list.length === 0) {
                        frame.status = "no agents"
                        frame.kicker = ""
                        frame.nodes = []
                        frame.links = []
                        return
                    }
                    const forest = frame.buildForest(list)
                    if (forest.length === 0) {
                        frame.status = "no agents"
                        frame.kicker = ""
                        frame.nodes = []
                        frame.links = []
                        return
                    }
                    const spacingX = 72
                    const spacingY = 64
                    const ring = Math.max(108, (forest.length * spacingX) / (Math.PI * 2))
                    const laid = frame.layoutCircle(forest, ring, spacingY * 0.82)
                    let minX = Infinity
                    let maxX = -Infinity
                    let minY = Infinity
                    let maxY = -Infinity
                    let live = 0
                    for (let i = 0; i < laid.nodes.length; i++) {
                        minX = Math.min(minX, laid.nodes[i].x)
                        maxX = Math.max(maxX, laid.nodes[i].x)
                        minY = Math.min(minY, laid.nodes[i].y)
                        maxY = Math.max(maxY, laid.nodes[i].y)
                        if (frame.isLive(laid.nodes[i].visual))
                            live += 1
                    }
                    frame.contentMinX = minX
                    frame.contentMinY = minY
                    frame.contentMaxX = maxX
                    frame.contentMaxY = maxY
                    frame.nodes = laid.nodes
                    frame.links = laid.links
                    frame.status = ""
                    const n = laid.nodes.length
                    frame.kicker = n === 1 ? "" : (n + " agents" + (live > 0 ? " · " + live + " live" : ""))
                } catch (e) {
                    frame.status = "bad agents payload"
                    frame.kicker = ""
                }
            }
            xhr.open("GET", Tokens.dimaagBase + "/agents")
            xhr.send()
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

            Text {
                renderType: Text.QtRendering
                anchors.centerIn: parent
                visible: frame.status !== "" || frame.nodes.length === 0
                text: frame.status !== "" ? frame.status : "Loading agents…"
                color: Tokens.inkGhost
                font.pixelSize: Tokens.typeBody
            }

            Item {
                id: ringView
                anchors.fill: parent
                visible: frame.status === "" && frame.nodes.length > 0

                readonly property real contentW: Math.max(frame.contentMaxX - frame.contentMinX, 0)
                readonly property real contentH: Math.max(frame.contentMaxY - frame.contentMinY, 0)
                readonly property real originX: width / 2 - (frame.contentMinX + contentW / 2)
                readonly property real originY: height / 2 - (frame.contentMinY + contentH / 2)

                Repeater {
                    model: frame.links
                    ShapeLink {
                        required property var modelData
                        x1: ringView.originX + modelData.x1
                        y1: ringView.originY + modelData.y1
                        x2: ringView.originX + modelData.x2
                        y2: ringView.originY + modelData.y2
                    }
                }

                Repeater {
                    model: frame.nodes
                    AgentDot {
                        required property var modelData
                        x: ringView.originX + modelData.x - width / 2
                        y: ringView.originY + modelData.y - anchorY
                        visual: modelData.visual
                        label: modelData.name
                        caption: modelData.caption
                    }
                }
            }
        }
    }
}

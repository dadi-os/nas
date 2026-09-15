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

        function leafCount(node) {
            if (!node.children || node.children.length === 0)
                return 1
            let n = 0
            for (let i = 0; i < node.children.length; i++)
                n += leafCount(node.children[i])
            return n
        }

        function visualOf(agent) {
            if (!agent.active)
                return "dormant"
            const lanes = agent.running || {}
            if (lanes.reasoning || lanes.conversation)
                return "running"
            return "idle"
        }

        function captionOf(agent) {
            if (!agent.active)
                return "dormant"
            const lanes = agent.running || {}
            if (lanes.reasoning)
                return "reasoning"
            if (lanes.conversation)
                return "talking"
            return "idle"
        }

        function buildTree(agents) {
            if (!agents || agents.length === 0)
                return null
            const byId = {}
            for (let i = 0; i < agents.length; i++)
                byId[agents[i].id] = agents[i]
            let rootAgent = null
            for (let i = 0; i < agents.length; i++) {
                if (agents[i].parent_agent_id === null) {
                    rootAgent = agents[i]
                    break
                }
            }
            if (!rootAgent)
                return null
            const childrenOf = {}
            for (let i = 0; i < agents.length; i++) {
                const agent = agents[i]
                if (agent.id === rootAgent.id)
                    continue
                let parentId = agent.parent_agent_id
                if (parentId === null || !byId[parentId])
                    parentId = rootAgent.id
                if (!childrenOf[parentId])
                    childrenOf[parentId] = []
                childrenOf[parentId].push(agent)
            }
            function toNode(agent) {
                const kids = childrenOf[agent.id] || []
                const node = {
                    id: agent.id,
                    name: agent.name,
                    visual: visualOf(agent),
                    caption: captionOf(agent),
                    children: []
                }
                for (let i = 0; i < kids.length; i++)
                    node.children.push(toNode(kids[i]))
                return node
            }
            return toNode(rootAgent)
        }

        function layoutTree(rootNode, spacingX, spacingY) {
            const nodes = []
            const links = []
            function walk(node, left, depth, parentPos) {
                const width = leafCount(node)
                const x = left + (width - 1) * spacingX / 2
                const y = depth * spacingY
                if (parentPos) {
                    links.push({
                        x1: parentPos.x,
                        y1: parentPos.y,
                        x2: x,
                        y2: y
                    })
                }
                nodes.push({
                    id: node.id,
                    name: node.name,
                    visual: node.visual,
                    caption: node.caption,
                    isRoot: depth === 0,
                    x: x,
                    y: y
                })
                let childLeft = left
                for (let i = 0; i < node.children.length; i++) {
                    const w = leafCount(node.children[i])
                    walk(node.children[i], childLeft, depth + 1, { x: x, y: y })
                    childLeft += w * spacingX
                }
            }
            walk(rootNode, 0, 0, null)
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
                    const tree = frame.buildTree(list)
                    if (!tree) {
                        frame.status = "no root agent"
                        frame.kicker = ""
                        frame.nodes = []
                        frame.links = []
                        return
                    }
                    const laid = frame.layoutTree(tree, 140, 96)
                    let minX = Infinity
                    let maxX = -Infinity
                    let minY = Infinity
                    let maxY = -Infinity
                    let running = 0
                    for (let i = 0; i < laid.nodes.length; i++) {
                        minX = Math.min(minX, laid.nodes[i].x)
                        maxX = Math.max(maxX, laid.nodes[i].x)
                        minY = Math.min(minY, laid.nodes[i].y)
                        maxY = Math.max(maxY, laid.nodes[i].y)
                        if (laid.nodes[i].visual === "running")
                            running += 1
                    }
                    frame.contentMinX = minX
                    frame.contentMinY = minY
                    frame.contentMaxX = maxX
                    frame.contentMaxY = maxY
                    frame.nodes = laid.nodes
                    frame.links = laid.links
                    frame.status = ""
                    const n = laid.nodes.length
                    frame.kicker = n === 1 ? "" : (n + " agents" + (running > 0 ? " · " + running + " running" : ""))
                } catch (e) {
                    frame.status = "bad agents payload"
                    frame.kicker = ""
                }
            }
            xhr.open("GET", Tokens.dimaagBase + "/agents")
            xhr.send()
        }

        Timer {
            interval: 2000
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
                color: "#b0b8a6"
                font.pixelSize: 13
            }

            Item {
                id: treeView
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
                        x1: treeView.originX + modelData.x1
                        y1: treeView.originY + modelData.y1
                        x2: treeView.originX + modelData.x2
                        y2: treeView.originY + modelData.y2
                    }
                }

                Repeater {
                    model: frame.nodes
                    AgentDot {
                        required property var modelData
                        x: treeView.originX + modelData.x - width / 2
                        y: treeView.originY + modelData.y - anchorY
                        visual: modelData.visual
                        isRoot: modelData.isRoot
                        label: modelData.name
                        caption: modelData.caption
                    }
                }
            }
        }
    }
}

import QtQuick

/**
 * DadiFlickable is a vertical scroller with natural (Mac) direction and light coast.
 * Trackpad pixel deltas move 1:1. Mouse notches step then decay; they do not stack flicks.
 * WheelHandler stays off the click path so list rows and buttons remain interactive.
 */
Flickable {
    id: root

    clip: true
    boundsBehavior: Flickable.DragAndOvershootBounds
    flickDeceleration: 2500
    maximumFlickVelocity: 2500
    flickableDirection: Flickable.VerticalFlick
    rebound: Transition {
        NumberAnimation {
            properties: "x,y"
            duration: 380
            easing.type: Easing.OutCubic
        }
    }

    property real coast: 0

    function clampY(y) {
        const maxY = Math.max(0, root.contentHeight - root.height)
        return Math.max(0, Math.min(maxY, y))
    }

    function applyWheel(pixelY, angleY) {
        if (root.contentHeight <= root.height)
            return
        const dy = pixelY !== 0 ? pixelY : angleY / 8
        if (dy === 0)
            return
        root.contentY = root.clampY(root.contentY + dy)
        if (pixelY !== 0)
            root.coast = 0
        else
            root.coast = dy * 0.28
    }

    onDraggingChanged: {
        if (dragging)
            coast = 0
    }

    Timer {
        interval: 16
        repeat: true
        running: Math.abs(root.coast) > 0.35
        onTriggered: {
            root.contentY = root.clampY(root.contentY + root.coast)
            root.coast *= 0.88
            if (Math.abs(root.coast) < 0.35)
                root.coast = 0
        }
    }

    WheelHandler {
        acceptedDevices: PointerDevice.Mouse | PointerDevice.TouchPad
        onWheel: function (event) {
            root.applyWheel(event.pixelDelta.y, event.angleDelta.y)
            event.accepted = true
        }
    }
}

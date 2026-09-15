import QtQuick
import QtQuick.Window

/**
 * FrostShell is the fill for plasmawindowed dadi apps.
 * Widgets sample wallpaper through Glass; these windows have no
 * containment, so frost is compositor blur plus a heavy white tint.
 *
 * plasmawindowed wraps the applet in a Rectangle filled with
 * Kirigami.Theme.backgroundColor. That plate is cleared so the tint
 * composites onto the desktop instead of an opaque theme fill.
 */
Item {
    id: root

    Rectangle {
        anchors.fill: parent
        color: Qt.rgba(1, 1, 1, 0.95)
    }

    Rectangle {
        anchors.fill: parent
        color: "transparent"
        border.color: "#ffffff"
        border.width: 1
        opacity: 0.38
    }

    function applyWindow() {
        const w = root.Window.window
        if (w)
            w.color = "transparent"

        let p = root.parent
        while (p) {
            if (p.color !== undefined)
                p.color = "transparent"
            p = p.parent
        }
    }

    property var win: Window.window
    onWinChanged: applyWindow()
    onParentChanged: applyWindow()
    Component.onCompleted: {
        applyWindow()
        Qt.callLater(applyWindow)
    }
}

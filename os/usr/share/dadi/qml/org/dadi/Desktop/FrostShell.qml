import QtQuick
import QtQuick.Window

/**
 * FrostShell is the fill for plasmawindowed dadi apps.
 * Desktop widgets sample wallpaper through Glass; standalone windows
 * do not have that containment, so this is a light frost plate.
 */
Item {
    id: root

    Rectangle {
        anchors.fill: parent
        color: "#fbfbfa"
    }

    Rectangle {
        anchors.fill: parent
        color: "transparent"
        border.color: "#ffffff"
        border.width: 1
        opacity: 0.55
    }

    function applyWindow() {
        const w = root.Window.window
        if (!w)
            return
        w.color = "#fbfbfa"
    }

    property var win: Window.window
    onWinChanged: applyWindow()
    Component.onCompleted: applyWindow()
}

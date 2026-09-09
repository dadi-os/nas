pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Layouts
import org.kde.plasma.plasmoid
import org.kde.plasma.core as PlasmaCore
import org.kde.plasma.plasma5support as Plasma5Support

PlasmoidItem {
    id: root
    preferredRepresentation: fullRepresentation
    Plasmoid.backgroundHints: PlasmaCore.Types.NoBackground

    fullRepresentation: CrestFrame {
        id: frame
        title: "Logs"
        Layout.minimumWidth: 300
        Layout.minimumHeight: 140
        Layout.preferredWidth: 380
        Layout.preferredHeight: 160

        property string line: "No recent errors"
        property string meta: ""
        property string err: ""

        function refresh() {
            const xhr = new XMLHttpRequest()
            xhr.onreadystatechange = function () {
                if (xhr.readyState !== XMLHttpRequest.DONE)
                    return
                if (xhr.status !== 200) {
                    frame.err = "nas unreachable"
                    return
                }
                try {
                    const data = JSON.parse(xhr.responseText)
                    const entries = data.entries || []
                    if (entries.length === 0) {
                        frame.line = "No recent errors"
                        frame.meta = ""
                        frame.err = ""
                        return
                    }
                    const e = entries[0]
                    frame.line = e.msg || e.raw || "(empty)"
                    frame.meta = (e.service || "") + " · " + (e.level || "")
                    frame.err = ""
                } catch (e) {
                    frame.err = "bad logs"
                }
            }
            xhr.open("GET", "http://127.0.0.1:8092/logs?level=error&limit=1")
            xhr.send()
        }

        Timer {
            interval: 10000
            running: true
            repeat: true
            triggeredOnStart: true
            onTriggered: frame.refresh()
        }

        MouseArea {
            anchors.fill: parent
            cursorShape: Qt.PointingHandCursor
            onClicked: {
                const ex = executable
                ex.exec("dadi-preferences")
            }
        }

        Plasma5Support.DataSource {
            id: executable
            engine: "executable"
            connectedSources: []
            onNewData: function (source, _data) {
                disconnectSource(source)
            }
            function exec(cmd) {
                connectSource(cmd)
            }
        }

        ColumnLayout {
            anchors.fill: parent
            spacing: 6

            Text {
                text: frame.err !== "" ? frame.err : frame.meta
                color: "#a8af9f"
                font.pixelSize: 11
                font.letterSpacing: 1.2
            }
            Text {
                text: frame.err !== "" ? "" : frame.line
                color: "#2c302a"
                font.pixelSize: 13
                wrapMode: Text.WordWrap
                maximumLineCount: 4
                elide: Text.ElideRight
                Layout.fillWidth: true
                Layout.fillHeight: true
            }
        }
    }
}

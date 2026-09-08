import QtQuick
import QtQuick.Controls
import QtQuick.Layouts

Item {
    ColumnLayout {
        anchors.fill: parent
        spacing: 12

        Text {
            text: "Desktop"
            color: "#2c302a"
            font.pixelSize: 22
            font.weight: Font.DemiBold
        }
        Text {
            text: "Wallpaper field for bone glass. Bloom is default — soft sage orb so veil panels catch light."
            color: "#6e7568"
            font.pixelSize: 12
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        Repeater {
            model: ["Bloom", "Mist", "Vein"]
            Button {
                required property string modelData
                Layout.fillWidth: true
                text: "Use " + modelData
                onClicked: wallpaperExec.exec("dadi-wallpaper " + modelData)
                background: Rectangle {
                    radius: 9
                    color: "#f7f9f4"
                    border.color: "#b9c9ab"
                    border.width: 1
                }
                contentItem: Text {
                    text: parent.text
                    color: "#5c6b52"
                    horizontalAlignment: Text.AlignHCenter
                    verticalAlignment: Text.AlignVCenter
                }
            }
        }

        Text {
            text: "Panel blur, dock autohide, and desktop icons-off are part of org.dadi.desktop. Reset layout: lookandfeeltool -a org.dadi.desktop --resetLayout"
            color: "#a8af9f"
            font.pixelSize: 11
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        Item { Layout.fillHeight: true }
    }

    // qml6 has no Plasma DataSource — use a tiny helper via Qt.openUrlExternally won't run shell.
    // Prefer Process if available; otherwise document CLI.
    Timer {
        id: wallpaperExec
        property string cmd: ""
        interval: 1
        repeat: false
        function exec(c) {
            // Write a oneshot user unit request via stdio is unavailable;
            // invoke through /bin/sh using Qt.application - fall back message
            console.log("run:", c)
            // QML Online: use XMLHttpRequest to local helper — skip; use StandardPaths
        }
    }

    Text {
        anchors.bottom: parent.bottom
        width: parent.width
        text: "From a terminal: dadi-wallpaper Bloom|Mist|Vein"
        color: "#b0b8a6"
        font.pixelSize: 11
    }
}

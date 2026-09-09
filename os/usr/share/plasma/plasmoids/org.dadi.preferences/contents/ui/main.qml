pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.plasma.plasmoid
import org.kde.plasma.core as PlasmaCore
import org.kde.plasma.plasma5support as Plasma5Support

PlasmoidItem {
    id: root

    preferredRepresentation: fullRepresentation
    toolTipMainText: "Preferences"
    Plasmoid.backgroundHints: PlasmaCore.Types.NoBackground

    fullRepresentation: Rectangle {
        id: win
        Layout.minimumWidth: 860
        Layout.minimumHeight: 560
        Layout.preferredWidth: 920
        Layout.preferredHeight: 640
        color: "#fafaf7"

        property string section: "dwar"
        property string toast: ""

        readonly property var sections: [
            { id: "dwar", label: "Dwar" },
            { id: "tunnel", label: "Tunnel" },
            { id: "devices", label: "Devices" },
            { id: "desktop", label: "Desktop" },
            { id: "power", label: "Power" }
        ]

        function showToast(msg) {
            toast = msg
            toastTimer.restart()
        }

        function sectionIndex() {
            for (let i = 0; i < sections.length; i++) {
                if (sections[i].id === section)
                    return i
            }
            return 0
        }

        Timer {
            id: toastTimer
            interval: 2400
            onTriggered: win.toast = ""
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

        RowLayout {
            anchors.fill: parent
            spacing: 0

            Rectangle {
                Layout.preferredWidth: 200
                Layout.fillHeight: true
                color: "#f7f9f4"

                Rectangle {
                    anchors.right: parent.right
                    width: 1
                    height: parent.height
                    color: "#e4ebdc"
                }

                ColumnLayout {
                    anchors.fill: parent
                    anchors.margins: 16
                    spacing: 6

                    Text {
                        text: "દાદી"
                        color: "#7e9270"
                        font.family: "Noto Sans Gujarati"
                        font.pixelSize: 28
                        font.weight: Font.Medium
                        Layout.bottomMargin: 12
                    }

                    Text {
                        text: "PREFERENCES"
                        color: "#a8af9f"
                        font.pixelSize: 10
                        font.letterSpacing: 2.5
                        Layout.bottomMargin: 8
                    }

                    Repeater {
                        model: win.sections
                        delegate: Item {
                            id: row
                            required property var modelData
                            Layout.fillWidth: true
                            height: 34
                            readonly property bool active: win.section === modelData.id

                            Rectangle {
                                anchors.fill: parent
                                radius: 9
                                color: row.active ? "#8fa38228" : "transparent"
                                border.color: row.active ? "#b9c9ab" : "transparent"
                                border.width: 1
                            }
                            Text {
                                anchors.fill: parent
                                anchors.leftMargin: 10
                                text: row.modelData.label
                                color: row.active ? "#5c6b52" : "#6e7568"
                                font.pixelSize: 13
                                font.weight: row.active ? Font.DemiBold : Font.Normal
                                verticalAlignment: Text.AlignVCenter
                            }
                            MouseArea {
                                anchors.fill: parent
                                cursorShape: Qt.PointingHandCursor
                                onClicked: win.section = row.modelData.id
                            }
                        }
                    }

                    Item { Layout.fillHeight: true }
                }
            }

            Rectangle {
                Layout.fillWidth: true
                Layout.fillHeight: true
                color: "#fafaf7"

                StackLayout {
                    anchors.fill: parent
                    anchors.margins: 24
                    currentIndex: win.sectionIndex()

                    ModulePane {
                        moduleName: "dwar"
                        showConfig: true
                        onSaved: msg => win.showToast(msg)
                    }
                    TunnelPane {
                        onSaved: msg => win.showToast(msg)
                    }
                    DevicesPane {
                        runner: executable
                    }
                    DesktopPane {
                        runner: executable
                        onToast: msg => win.showToast(msg)
                    }
                    PowerPane {}
                }
            }
        }

        Rectangle {
            anchors.horizontalCenter: parent.horizontalCenter
            anchors.bottom: parent.bottom
            anchors.bottomMargin: 24
            visible: win.toast !== ""
            width: toastLabel.width + 28
            height: toastLabel.height + 16
            radius: 9
            color: "#8fa382"
            Text {
                id: toastLabel
                anchors.centerIn: parent
                text: win.toast
                color: "#fafaf7"
                font.pixelSize: 12
            }
        }
    }
}

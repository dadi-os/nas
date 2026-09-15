pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.plasma.plasmoid
import org.kde.plasma.core as PlasmaCore
import org.kde.plasma.plasma5support as Plasma5Support
import org.dadi.Desktop

PlasmoidItem {
    id: root

    preferredRepresentation: fullRepresentation
    toolTipMainText: "Preferences"
    Plasmoid.backgroundHints: PlasmaCore.Types.NoBackground

    fullRepresentation: Item {
        id: win
        Layout.minimumWidth: 960
        Layout.minimumHeight: 620
        Layout.preferredWidth: 1120
        Layout.preferredHeight: 780

        property string section: "users"
        property string toast: ""
        palette.accent: "#141511"
        palette.highlight: "#141511"
        palette.highlightedText: "#ffffff"
        palette.text: "#141511"
        palette.windowText: "#141511"
        palette.button: "#141511"
        palette.buttonText: "#ffffff"
        palette.link: "#141511"

        readonly property var sections: [
            { id: "users", label: "Users" },
            { id: "dwar", label: "Dwar" },
            { id: "devices", label: "Devices" },
            { id: "desktop", label: "Desktop" }
        ]

        FrostShell { anchors.fill: parent }

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

            Item {
                Layout.preferredWidth: 200
                Layout.fillHeight: true

                Rectangle {
                    anchors.right: parent.right
                    width: 1
                    height: parent.height
                    color: "#14151118"
                }

                ColumnLayout {
                    anchors.fill: parent
                    anchors.margins: 16
                    spacing: 4

                    Text {
                        text: "દાદી"
                        color: "#141511"
                        font.family: "Noto Sans Gujarati"
                        font.pixelSize: 26
                        font.weight: Font.Medium
                        Layout.bottomMargin: 16
                    }

                    Repeater {
                        model: win.sections
                        delegate: Item {
                            id: row
                            required property var modelData
                            Layout.fillWidth: true
                            height: 36
                            readonly property bool active: win.section === modelData.id

                            Rectangle {
                                anchors.fill: parent
                                radius: 10
                                color: row.active ? "#14151112" : "transparent"
                            }

                            Text {
                                anchors.fill: parent
                                anchors.leftMargin: 12
                                text: row.modelData.label
                                color: "#141511"
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

            Item {
                Layout.fillWidth: true
                Layout.fillHeight: true

                StackLayout {
                    anchors.fill: parent
                    anchors.margins: 24
                    currentIndex: win.sectionIndex()

                    AccessPane {
                        onSaved: msg => win.showToast(msg)
                    }
                    ModulePane {
                        onSaved: msg => win.showToast(msg)
                    }
                    DevicesPane {}
                    DesktopPane {
                        runner: executable
                        onToast: msg => win.showToast(msg)
                    }
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
            radius: 10
            color: "#141511"
            Text {
                id: toastLabel
                anchors.centerIn: parent
                text: win.toast
                color: "#ffffff"
                font.pixelSize: 12
            }
        }
    }
}

pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import org.kde.plasma.plasmoid
import org.kde.plasma.plasma5support as Plasma5Support

PlasmoidItem {
    id: root

    preferredRepresentation: fullRepresentation
    toolTipMainText: "દાદી"
    toolTipSubText: "Preferences"

    fullRepresentation: Item {
        id: body
        Layout.minimumWidth: mark.implicitWidth + Kirigami.Units.smallSpacing * 3
        Layout.preferredWidth: Layout.minimumWidth
        Layout.fillHeight: true

        Text {
            id: mark
            anchors.centerIn: parent
            anchors.verticalCenterOffset: Math.round(font.pixelSize * 0.2)
            text: "દાદી"
            color: "#141511"
            font.family: "Noto Sans Gujarati"
            font.weight: Font.Medium
            font.pixelSize: Math.max(17, Math.round(body.height * 0.58))
            Accessible.name: "દાદી"
        }

        MouseArea {
            anchors.fill: parent
            acceptedButtons: Qt.LeftButton | Qt.RightButton
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onClicked: function (mouse) {
                if (mouse.button === Qt.RightButton)
                    brandMenu.popup(body, 0, body.height + 4)
                else
                    executable.exec("dadi-preferences")
            }
        }

        Menu {
            id: brandMenu
            padding: 8
            delegate: MenuItem {
                implicitHeight: 34
                leftPadding: 12
                rightPadding: 14
                background: Rectangle {
                    radius: 8
                    color: parent.highlighted ? "#14151112" : "transparent"
                }
                contentItem: Text {
                    text: parent.text
                    color: "#141511"
                    font.pixelSize: 13
                    verticalAlignment: Text.AlignVCenter
                }
            }

            background: Rectangle {
                radius: 14
                color: Qt.rgba(255 / 255, 255 / 255, 255 / 255, 0.82)
                border.color: Qt.rgba(255 / 255, 255 / 255, 255 / 255, 0.45)
                border.width: 1
            }

            MenuItem {
                text: "About દાદી"
                onTriggered: aboutDialog.open()
            }
            MenuSeparator {
                contentItem: Rectangle {
                    implicitHeight: 1
                    color: "#14151114"
                }
                background: null
            }
            MenuItem {
                text: "Restart…"
                onTriggered: executable.exec("systemctl reboot")
            }
            MenuItem {
                text: "Shut Down…"
                onTriggered: executable.exec("systemctl poweroff")
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

        Dialog {
            id: aboutDialog
            title: "દાદી"
            modal: true
            width: 360
            padding: 20
            standardButtons: Dialog.NoButton

            background: Rectangle {
                radius: 16
                color: "#fbfbfa"
                border.color: "#ffffff"
                border.width: 1
            }

            contentItem: ColumnLayout {
                spacing: 10
                Text {
                    text: "દાદી"
                    color: "#141511"
                    font.family: "Noto Sans Gujarati"
                    font.pixelSize: 36
                    font.weight: Font.Medium
                    Layout.alignment: Qt.AlignHCenter
                }
                Text {
                    text: "Plasma desktop for the dadi box"
                    color: "#8a8e87"
                    font.pixelSize: 13
                    Layout.alignment: Qt.AlignHCenter
                }
                Text {
                    text: "Hath lives on other devices. This machine is the OS."
                    color: "#8a8e87"
                    font.pixelSize: 12
                    wrapMode: Text.WordWrap
                    Layout.fillWidth: true
                    horizontalAlignment: Text.AlignHCenter
                }
                Rectangle {
                    Layout.topMargin: 8
                    Layout.alignment: Qt.AlignHCenter
                    implicitWidth: okLabel.width + 32
                    implicitHeight: 36
                    radius: 10
                    color: okPress.containsMouse && okPress.pressed ? "#000000" : "#141511"
                    Text {
                        id: okLabel
                        anchors.centerIn: parent
                        text: "OK"
                        color: "#ffffff"
                        font.pixelSize: 13
                        font.weight: Font.DemiBold
                    }
                    MouseArea {
                        id: okPress
                        anchors.fill: parent
                        hoverEnabled: true
                        cursorShape: Qt.PointingHandCursor
                        onClicked: aboutDialog.close()
                    }
                }
            }
        }
    }
}

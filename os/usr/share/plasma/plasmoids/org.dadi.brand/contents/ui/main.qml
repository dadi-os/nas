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
    toolTipSubText: "Menu"

    fullRepresentation: Item {
        id: body
        Layout.minimumWidth: mark.implicitWidth + Kirigami.Units.smallSpacing * 3
        Layout.preferredWidth: Layout.minimumWidth
        Layout.fillHeight: true

        Text {
            id: mark
            anchors.centerIn: parent
            text: "દાદી"
            color: "#7e9270"
            font.family: "Noto Sans Gujarati"
            font.weight: Font.Medium
            font.pixelSize: Math.max(17, Math.round(body.height * 0.58))
            Accessible.name: "દાદી"
        }

        MouseArea {
            anchors.fill: parent
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onClicked: brandMenu.popup(body, 0, body.height + 4)
        }

        Menu {
            id: brandMenu
            padding: 8

            background: Rectangle {
                radius: 12
                color: Qt.rgba(250 / 255, 250 / 255, 247 / 255, 0.92)
                border.color: "#b9c9ab"
                border.width: 1
            }

            MenuItem {
                text: "Add Device…"
                onTriggered: executable.exec("dadi-add-device")
            }
            MenuItem {
                text: "Preferences…"
                onTriggered: executable.exec("dadi-preferences")
            }
            MenuSeparator {}
            MenuItem {
                text: "About દાદી"
                onTriggered: aboutDialog.open()
            }
            MenuSeparator {}
            MenuItem {
                text: "Lock Screen"
                onTriggered: executable.exec("loginctl lock-session")
            }
            MenuItem {
                text: "Sleep Display"
                onTriggered: executable.exec("kscreen-doctor --dpms off")
            }
            MenuSeparator {}
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
            standardButtons: Dialog.Ok
            modal: true
            width: 360

            contentItem: ColumnLayout {
                spacing: 10
                Text {
                    text: "દાદી"
                    color: "#7e9270"
                    font.family: "Noto Sans Gujarati"
                    font.pixelSize: 36
                    font.weight: Font.Medium
                    Layout.alignment: Qt.AlignHCenter
                }
                Text {
                    text: "bone glass · Plasma desktop for the dadi box"
                    color: "#6e7568"
                    font.pixelSize: 12
                    Layout.alignment: Qt.AlignHCenter
                }
                Text {
                    text: "Hath lives on other devices. This machine is the OS."
                    color: "#a8af9f"
                    font.pixelSize: 11
                    wrapMode: Text.WordWrap
                    Layout.fillWidth: true
                    horizontalAlignment: Text.AlignHCenter
                }
            }
        }
    }
}

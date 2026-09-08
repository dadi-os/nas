pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Layouts
import org.kde.kirigami as Kirigami
import org.kde.plasma.plasmoid

PlasmoidItem {
    id: root

    preferredRepresentation: fullRepresentation
    compactRepresentation: null

    fullRepresentation: Item {
        id: body

        Layout.minimumWidth: mark.implicitWidth + Kirigami.Units.smallSpacing * 2
        Layout.preferredWidth: Layout.minimumWidth
        Layout.minimumHeight: Kirigami.Units.iconSizes.small
        Layout.fillHeight: true

        Text {
            id: mark
            anchors.centerIn: parent
            text: "દાદી"
            color: "#7e9270"
            font.family: "Noto Sans Gujarati"
            font.weight: Font.Medium
            font.pixelSize: Math.max(16, Math.round(body.height * 0.62))
            verticalAlignment: Text.AlignVCenter
            Accessible.name: "દાદી"
        }
    }
}

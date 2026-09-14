import QtQuick
import QtQuick.Controls
import QtQuick.Layouts

ColumnLayout {
    id: root
    property string label: ""
    property string hint: ""
    property alias text: field.text
    property bool secret: false
    Layout.fillWidth: true
    spacing: 6

    Text {
        text: root.label
        color: "#2c302a"
        font.pixelSize: 13
        visible: root.label !== ""
    }
    Text {
        text: root.hint
        color: "#6e7568"
        font.pixelSize: 12
        wrapMode: Text.WordWrap
        Layout.fillWidth: true
        visible: root.hint !== ""
    }
    TextField {
        id: field
        Layout.fillWidth: true
        Layout.preferredHeight: 40
        echoMode: root.secret ? TextInput.Password : TextInput.Normal
        font.pixelSize: 13
        color: "#2c302a"
        background: Rectangle {
            radius: 9
            color: "#f7f9f4"
            border.color: field.activeFocus ? "#8fa382" : "#b9c9ab"
            border.width: 1
        }
    }
}

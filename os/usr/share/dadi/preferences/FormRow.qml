import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.dadi.Desktop

ColumnLayout {
    id: root
    property string label: ""
    property string hint: ""
    property alias text: field.text
    property bool editable: true
    property bool secret: false
    Layout.fillWidth: true
    spacing: 6

    Text {
        text: root.label
        color: "#141511"
        font.pixelSize: 13
        visible: root.label !== ""
    }
    Text {
        text: root.hint
        color: "#8a8e87"
        font.pixelSize: 12
        wrapMode: Text.WordWrap
        Layout.fillWidth: true
        visible: root.hint !== ""
    }
    TextField {
        id: field
        Layout.fillWidth: true
        Layout.preferredHeight: 40
        leftPadding: 12
        rightPadding: 12
        echoMode: root.secret ? TextInput.Password : TextInput.Normal
        font.pixelSize: 13
        color: "#141511"
        selectByMouse: true
        enabled: root.editable
        hoverEnabled: true
        palette.accent: "#141511"
        palette.highlight: "#141511"
        palette.highlightedText: "#ffffff"
        palette.text: "#141511"
        palette.placeholderText: "#8a8e87"
        selectionColor: "#141511"
        selectedTextColor: "#ffffff"
        background: Rectangle {
            radius: 10
            color: root.editable ? Tokens.sageFill : "#14151108"
            border.color: field.activeFocus ? "#141511" : Tokens.sageStroke
            border.width: 1
        }
    }
}

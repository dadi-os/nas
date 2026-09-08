import QtQuick
import QtQuick.Controls
import QtQuick.Layouts

Item {
    id: root

    property string moduleName: "dwar"
    property bool showConfig: false
    signal saved(string message)

    property string envText: ""
    property string configText: ""
    property string status: ""
    property bool loading: false

    function load() {
        loading = true
        status = ""
        const xhr = new XMLHttpRequest()
        xhr.onreadystatechange = function () {
            if (xhr.readyState !== XMLHttpRequest.DONE)
                return
            loading = false
            if (xhr.status !== 200) {
                status = "Failed to load .env (" + xhr.status + ")"
                return
            }
            envText = xhr.responseText
        }
        xhr.open("GET", "http://127.0.0.1:8092/modules/" + moduleName + "/env")
        xhr.send()

        if (showConfig) {
            const xhr2 = new XMLHttpRequest()
            xhr2.onreadystatechange = function () {
                if (xhr2.readyState !== XMLHttpRequest.DONE)
                    return
                if (xhr2.status === 200)
                    configText = xhr2.responseText
            }
            xhr2.open("GET", "http://127.0.0.1:8092/modules/dwar/config")
            xhr2.send()
        }
    }

    function saveEnv() {
        status = "Saving…"
        const xhr = new XMLHttpRequest()
        xhr.onreadystatechange = function () {
            if (xhr.readyState !== XMLHttpRequest.DONE)
                return
            if (xhr.status !== 200) {
                status = "Save failed (" + xhr.status + ")"
                return
            }
            status = ""
            root.saved(moduleName + " env saved · restarted")
        }
        xhr.open("PUT", "http://127.0.0.1:8092/modules/" + moduleName + "/env")
        xhr.setRequestHeader("Content-Type", "text/plain; charset=utf-8")
        xhr.send(envText)
    }

    function saveConfig() {
        status = "Saving config…"
        const xhr = new XMLHttpRequest()
        xhr.onreadystatechange = function () {
            if (xhr.readyState !== XMLHttpRequest.DONE)
                return
            if (xhr.status !== 200) {
                status = "Config save failed (" + xhr.status + ")"
                return
            }
            status = ""
            root.saved("dwar config saved · restarted")
        }
        xhr.open("PUT", "http://127.0.0.1:8092/modules/dwar/config")
        xhr.setRequestHeader("Content-Type", "text/plain; charset=utf-8")
        xhr.send(configText)
    }

    Component.onCompleted: load()
    onModuleNameChanged: load()
    onVisibleChanged: if (visible) load()

    ColumnLayout {
        anchors.fill: parent
        spacing: 12

        Text {
            text: moduleName.charAt(0).toUpperCase() + moduleName.slice(1)
            color: "#2c302a"
            font.pixelSize: 22
            font.weight: Font.DemiBold
        }
        Text {
            text: "Environment and timing live under /var/lib/dadi and survive reboot."
            color: "#6e7568"
            font.pixelSize: 12
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        Text {
            text: ".ENV"
            color: "#5c6b52"
            font.pixelSize: 10
            font.letterSpacing: 2
        }

        ScrollView {
            Layout.fillWidth: true
            Layout.fillHeight: true
            Layout.preferredHeight: showConfig ? parent.height * 0.35 : parent.height * 0.7
            TextArea {
                id: envArea
                width: parent.availableWidth
                wrapMode: TextEdit.NoWrap
                text: root.envText
                onTextChanged: root.envText = text
                font.family: "Noto Sans Mono"
                font.pixelSize: 12
                color: "#2c302a"
                background: Rectangle {
                    radius: 9
                    color: "#f7f9f4"
                    border.color: "#b9c9ab"
                    border.width: 1
                }
            }
        }

        Button {
            text: "Save .env"
            onClicked: root.saveEnv()
            background: Rectangle {
                radius: 9
                color: parent.down ? "#5c6b52" : "#8fa382"
            }
            contentItem: Text {
                text: parent.text
                color: "#fafaf7"
                horizontalAlignment: Text.AlignHCenter
                verticalAlignment: Text.AlignVCenter
                font.pixelSize: 12
            }
        }

        Text {
            visible: showConfig
            text: "CONFIG.TOML"
            color: "#5c6b52"
            font.pixelSize: 10
            font.letterSpacing: 2
        }

        ScrollView {
            visible: showConfig
            Layout.fillWidth: true
            Layout.fillHeight: true
            TextArea {
                width: parent.availableWidth
                wrapMode: TextEdit.NoWrap
                text: root.configText
                onTextChanged: root.configText = text
                font.family: "Noto Sans Mono"
                font.pixelSize: 12
                color: "#2c302a"
                background: Rectangle {
                    radius: 9
                    color: "#f7f9f4"
                    border.color: "#b9c9ab"
                    border.width: 1
                }
            }
        }

        Button {
            visible: showConfig
            text: "Save config.toml"
            onClicked: root.saveConfig()
            background: Rectangle {
                radius: 9
                color: parent.down ? "#5c6b52" : "#8fa382"
            }
            contentItem: Text {
                text: parent.text
                color: "#fafaf7"
                horizontalAlignment: Text.AlignHCenter
                verticalAlignment: Text.AlignVCenter
                font.pixelSize: 12
            }
        }

        Text {
            text: status
            color: "#6e7568"
            font.pixelSize: 11
            visible: status !== ""
        }
    }
}

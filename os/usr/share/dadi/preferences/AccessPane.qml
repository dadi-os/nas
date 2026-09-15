import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import org.dadi.Desktop

Item {
    id: root
    signal saved(string message)

    property var users: []
    property string selected: ""
    property bool adding: true
    property string status: ""

    function load() {
        const xhr = new XMLHttpRequest()
        xhr.onreadystatechange = function () {
            if (xhr.readyState !== XMLHttpRequest.DONE)
                return
            if (xhr.status !== 200) {
                status = "Failed to load users (" + xhr.status + ")"
                return
            }
            try {
                const data = JSON.parse(xhr.responseText)
                if (!Array.isArray(data.users)) {
                    status = "Bad users payload"
                    return
                }
                users = data.users
                if (!adding) {
                    let found = false
                    for (let i = 0; i < users.length; i++) {
                        if (users[i].name === selected) {
                            found = true
                            break
                        }
                    }
                    if (!found)
                        startAdd()
                }
            } catch (e) {
                status = "Bad users payload"
            }
        }
        xhr.open("GET", Tokens.nasBase + "/access")
        xhr.send()
    }

    function startAdd() {
        adding = true
        selected = ""
        userName.text = ""
        passwordField.text = ""
        confirmField.text = ""
        status = ""
    }

    function selectUser(name) {
        adding = false
        selected = name
        userName.text = name
        passwordField.text = ""
        confirmField.text = ""
        status = ""
    }

    function saveUser() {
        const username = userName.text.trim()
        const password = passwordField.text
        const confirm = confirmField.text
        if (password !== confirm) {
            status = "Passwords do not match"
            return
        }
        status = "Saving…"
        const xhr = new XMLHttpRequest()
        xhr.onreadystatechange = function () {
            if (xhr.readyState !== XMLHttpRequest.DONE)
                return
            if (xhr.status !== 200) {
                try {
                    const data = JSON.parse(xhr.responseText)
                    status = (data.error && data.error.message)
                            ? data.error.message
                            : ("Save failed (" + xhr.status + ")")
                } catch (e) {
                    status = "Save failed (" + xhr.status + ")"
                }
                return
            }
            try {
                const data = JSON.parse(xhr.responseText)
                status = ""
                passwordField.text = ""
                confirmField.text = ""
                if (data.username)
                    userName.text = data.username
                adding = false
                selected = data.username
                root.saved(data.created ? "User added" : "Password saved")
                root.load()
            } catch (e) {
                status = "Bad users payload"
            }
        }
        xhr.open("POST", Tokens.nasBase + "/access/users")
        xhr.setRequestHeader("Content-Type", "application/json")
        xhr.send(JSON.stringify({ username: username, password: password }))
    }

    function removeUser() {
        const username = selected
        if (username === "")
            return
        status = "Removing…"
        const xhr = new XMLHttpRequest()
        xhr.onreadystatechange = function () {
            if (xhr.readyState !== XMLHttpRequest.DONE)
                return
            if (xhr.status !== 200) {
                try {
                    const data = JSON.parse(xhr.responseText)
                    status = (data.error && data.error.message)
                            ? data.error.message
                            : ("Remove failed (" + xhr.status + ")")
                } catch (e) {
                    status = "Remove failed (" + xhr.status + ")"
                }
                return
            }
            status = ""
            root.saved("User removed")
            startAdd()
            root.load()
        }
        xhr.open("DELETE", Tokens.nasBase + "/access/users/" + encodeURIComponent(username))
        xhr.send()
    }

    Component.onCompleted: load()
    onVisibleChanged: if (visible) load()

    ColumnLayout {
        anchors.fill: parent
        spacing: 14

        Text {
            text: "Users"
            color: "#141511"
            font.pixelSize: 22
            font.weight: Font.DemiBold
            font.letterSpacing: -0.3
        }
        Text {
            text: "SSH logins on this box. dadi is the session and cannot sign in remotely."
            color: "#8a8e87"
            font.pixelSize: 13
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        DadiFlickable {
            Layout.fillWidth: true
            Layout.preferredHeight: Math.max(52, Math.min(220, userCol.height))
            contentWidth: width
            contentHeight: userCol.height

            ColumnLayout {
                id: userCol
                width: parent.width
                spacing: 6

                Text {
                    visible: users.length === 0
                    text: "No users yet. Add one to administer the box over SSH."
                    color: "#8a8e87"
                    font.pixelSize: 13
                    wrapMode: Text.WordWrap
                    Layout.fillWidth: true
                }

                Repeater {
                    model: root.users
                    delegate: Rectangle {
                        id: row
                        required property var modelData
                        Layout.fillWidth: true
                        implicitHeight: 44
                        radius: 10
                        color: root.selected === modelData.name ? "#14151112" : "transparent"

                        MouseArea {
                            id: hover
                            anchors.fill: parent
                            hoverEnabled: true
                            cursorShape: Qt.PointingHandCursor
                            onClicked: root.selectUser(row.modelData.name)
                        }

                        Rectangle {
                            anchors.fill: parent
                            radius: 10
                            color: "#1415110a"
                            visible: hover.containsMouse && root.selected !== row.modelData.name
                        }

                        RowLayout {
                            anchors.fill: parent
                            anchors.leftMargin: 12
                            anchors.rightMargin: 12
                            spacing: 8

                            Text {
                                text: row.modelData.name
                                color: "#141511"
                                font.pixelSize: 14
                                font.weight: Font.DemiBold
                            }
                            Item { Layout.fillWidth: true }
                            Text {
                                text: row.modelData.password_set ? "" : "No password"
                                color: "#8a8e87"
                                font.pixelSize: 12
                            }
                        }
                    }
                }

                DadiButton {
                    kind: "ghost"
                    text: "Add user"
                    Layout.alignment: Qt.AlignLeft
                    onClicked: root.startAdd()
                }
            }
        }

        Text {
            text: root.adding ? "New user" : ("Password for " + root.selected)
            color: "#141511"
            font.pixelSize: 13
            font.weight: Font.DemiBold
            Layout.topMargin: 4
        }
        FormRow {
            id: userName
            label: "Username"
            editable: root.adding
        }
        FormRow {
            id: passwordField
            label: "Password"
            secret: true
        }
        FormRow {
            id: confirmField
            label: "Confirm"
            secret: true
        }

        RowLayout {
            spacing: 10
            DadiButton {
                text: root.adding ? "Add user" : "Save password"
                enabled: userName.text.trim().length >= 2 && passwordField.text.length >= 8 && passwordField.text === confirmField.text
                onClicked: root.saveUser()
            }
            DadiButton {
                kind: "danger"
                text: "Remove"
                visible: !root.adding && root.selected !== ""
                onClicked: root.removeUser()
            }
        }

        Text {
            text: status
            color: "#c45c4a"
            font.pixelSize: 12
            visible: status !== ""
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        Item { Layout.fillHeight: true }
    }
}

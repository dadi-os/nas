import QtQuick
import QtQuick.Controls
import QtQuick.Layouts

Item {
    id: root
    signal saved(string message)

    property var users: []
    property bool tpmPresent: false
    property bool tpmEnrolled: false
    property string status: ""

    function load() {
        const xhr = new XMLHttpRequest()
        xhr.onreadystatechange = function () {
            if (xhr.readyState !== XMLHttpRequest.DONE)
                return
            if (xhr.status !== 200) {
                status = "Failed to load access status (" + xhr.status + ")"
                return
            }
            try {
                const data = JSON.parse(xhr.responseText)
                if (!Array.isArray(data.users)) {
                    status = "Bad access payload"
                    return
                }
                users = data.users
                tpmPresent = !!(data.tpm && data.tpm.present)
                tpmEnrolled = !!(data.tpm && data.tpm.enrolled)
            } catch (e) {
                status = "Bad access payload"
            }
        }
        xhr.open("GET", "http://127.0.0.1:8092/access")
        xhr.send()
    }

    function userExists() {
        const name = userName.text.trim()
        for (let i = 0; i < users.length; i++) {
            if (users[i].name === name)
                return true
        }
        return false
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
                    status = data.error.message
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
                root.saved(data.created ? "SSH user added" : "SSH password saved")
                root.load()
            } catch (e) {
                status = "Bad access payload"
            }
        }
        xhr.open("POST", "http://127.0.0.1:8092/access/users")
        xhr.setRequestHeader("Content-Type", "application/json")
        xhr.send(JSON.stringify({ username: username, password: password }))
    }

    Component.onCompleted: load()
    onVisibleChanged: if (visible) load()

    ColumnLayout {
        anchors.fill: parent
        spacing: 14

        Text {
            text: "Access"
            color: "#2c302a"
            font.pixelSize: 20
            font.weight: Font.DemiBold
        }
        Text {
            text: "The box boots as dadi with no password. Add an SSH user here to administer the box remotely. dadi cannot SSH."
            color: "#6e7568"
            font.pixelSize: 13
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        Text {
            text: "SSH USERS"
            color: "#5c6b52"
            font.pixelSize: 10
            font.letterSpacing: 2
            Layout.topMargin: 4
        }
        Text {
            visible: users.length === 0
            text: "No SSH users yet. Nothing can sign in over SSH until you add one."
            color: "#6e7568"
            font.pixelSize: 12
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }
        Repeater {
            model: root.users
            Text {
                required property var modelData
                text: modelData.name + (modelData.password_set ? "" : " — no password")
                color: "#2c302a"
                font.pixelSize: 13
            }
        }

        Text {
            text: "ADD USER"
            color: "#5c6b52"
            font.pixelSize: 10
            font.letterSpacing: 2
            Layout.topMargin: 8
        }
        FormRow {
            id: userName
            label: "Username"
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
        Button {
            Layout.preferredHeight: 40
            Layout.preferredWidth: 160
            enabled: userName.text.trim().length >= 2 && passwordField.text.length >= 8 && passwordField.text === confirmField.text
            onClicked: root.saveUser()
            background: Rectangle {
                radius: 9
                color: parent.down ? "#5c6b52" : (parent.enabled ? "#8fa382" : "#d5ddcb")
            }
            contentItem: Text {
                text: root.userExists() ? "Set password" : "Add user"
                color: "#fafaf7"
                horizontalAlignment: Text.AlignHCenter
                verticalAlignment: Text.AlignVCenter
                font.pixelSize: 13
            }
        }

        Text {
            text: "DISK UNLOCK"
            color: "#5c6b52"
            font.pixelSize: 10
            font.letterSpacing: 2
            Layout.topMargin: 8
        }
        Text {
            text: tpmEnrolled
                  ? "TPM2 PCR 7 is enrolled. Reboots unlock the disk without typing the recovery passphrase."
                  : (tpmPresent
                     ? "TPM is present but not enrolled yet. After the first LUKS passphrase, dadi-tpm-enroll seals the volume."
                     : "No TPM reported. The recovery passphrase is required at every boot.")
            color: "#6e7568"
            font.pixelSize: 13
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        Text {
            text: status
            color: "#b56b5c"
            font.pixelSize: 12
            visible: status !== ""
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        Item { Layout.fillHeight: true }
    }
}

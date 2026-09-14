import QtQuick
import QtQuick.Controls
import QtQuick.Layouts

Item {
    id: root
    signal saved(string message)

    property bool passwordSet: false
    property bool tpmPresent: false
    property bool tpmEnrolled: false
    property string status: ""
    property string password: ""
    property string confirm: ""

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
                passwordSet = !!data.password_set
                tpmPresent = !!(data.tpm && data.tpm.present)
                tpmEnrolled = !!(data.tpm && data.tpm.enrolled)
            } catch (e) {
                status = "Bad access payload"
            }
        }
        xhr.open("GET", "http://127.0.0.1:8092/access")
        xhr.send()
    }

    function savePassword() {
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
                status = "Save failed (" + xhr.status + ")"
                return
            }
            status = ""
            password = ""
            confirm = ""
            passwordSet = true
            root.saved("ankur SSH password saved")
        }
        xhr.open("PUT", "http://127.0.0.1:8092/access/ssh-password")
        xhr.setRequestHeader("Content-Type", "application/json")
        xhr.send(JSON.stringify({ password: password }))
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
            text: "The box boots as dadi with no password. SSH is ankur only."
            color: "#6e7568"
            font.pixelSize: 13
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        Text {
            text: "SSH PASSWORD"
            color: "#5c6b52"
            font.pixelSize: 10
            font.letterSpacing: 2
            Layout.topMargin: 4
        }
        Text {
            text: passwordSet ? "Password is set for ankur. Saving replaces it." : "No password yet — ankur cannot sign in with a password until you set one."
            color: "#6e7568"
            font.pixelSize: 12
            wrapMode: Text.WordWrap
            Layout.fillWidth: true
        }

        Text {
            text: "Password"
            color: "#2c302a"
            font.pixelSize: 13
        }
        TextField {
            Layout.fillWidth: true
            Layout.preferredHeight: 40
            echoMode: TextInput.Password
            text: root.password
            onTextChanged: root.password = text
            font.pixelSize: 13
            color: "#2c302a"
            background: Rectangle {
                radius: 9
                color: "#f7f9f4"
                border.color: "#b9c9ab"
                border.width: 1
            }
        }
        Text {
            text: "Confirm"
            color: "#2c302a"
            font.pixelSize: 13
        }
        TextField {
            Layout.fillWidth: true
            Layout.preferredHeight: 40
            echoMode: TextInput.Password
            text: root.confirm
            onTextChanged: root.confirm = text
            font.pixelSize: 13
            color: "#2c302a"
            background: Rectangle {
                radius: 9
                color: "#f7f9f4"
                border.color: "#b9c9ab"
                border.width: 1
            }
        }
        Button {
            Layout.preferredHeight: 40
            Layout.preferredWidth: 160
            enabled: root.password.length >= 8 && root.password === root.confirm
            onClicked: root.savePassword()
            background: Rectangle {
                radius: 9
                color: parent.down ? "#5c6b52" : (parent.enabled ? "#8fa382" : "#d5ddcb")
            }
            contentItem: Text {
                text: "Save password"
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

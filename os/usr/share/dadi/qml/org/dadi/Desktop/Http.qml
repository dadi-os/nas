import QtQuick

QtObject {
    id: root

    function get(url, callback) {
        const xhr = new XMLHttpRequest()
        xhr.onreadystatechange = function () {
            if (xhr.readyState !== XMLHttpRequest.DONE)
                return
            callback(xhr.status, xhr.responseText)
        }
        xhr.open("GET", url)
        xhr.send()
    }

    function putText(url, body, callback) {
        const xhr = new XMLHttpRequest()
        xhr.onreadystatechange = function () {
            if (xhr.readyState !== XMLHttpRequest.DONE)
                return
            callback(xhr.status, xhr.responseText)
        }
        xhr.open("PUT", url)
        xhr.setRequestHeader("Content-Type", "text/plain; charset=utf-8")
        xhr.send(body)
    }

    function post(url, callback) {
        const xhr = new XMLHttpRequest()
        xhr.onreadystatechange = function () {
            if (xhr.readyState !== XMLHttpRequest.DONE)
                return
            callback(xhr.status, xhr.responseText)
        }
        xhr.open("POST", url)
        xhr.send()
    }

    function postJson(url, obj, callback) {
        const xhr = new XMLHttpRequest()
        xhr.onreadystatechange = function () {
            if (xhr.readyState !== XMLHttpRequest.DONE)
                return
            callback(xhr.status, xhr.responseText)
        }
        xhr.open("POST", url)
        xhr.setRequestHeader("Content-Type", "application/json")
        xhr.send(JSON.stringify(obj))
    }
}

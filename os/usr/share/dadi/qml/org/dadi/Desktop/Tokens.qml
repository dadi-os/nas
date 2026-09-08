pragma Singleton
import QtQuick

QtObject {
    readonly property color bone: "#fafaf7"
    readonly property color boneRaised: "#f7f9f4"
    readonly property color sage: "#8fa382"
    readonly property color sageText: "#7e9270"
    readonly property color sageDeep: "#5c6b52"
    readonly property color sageLine: "#b9c9ab"
    readonly property color sageFaint: "#8fa38222"
    readonly property color ink: "#2c302a"
    readonly property color inkMuted: "#6e7568"
    readonly property color inkGhost: "#b0b8a6"
    readonly property color rule: "#e4ebdc"
    readonly property real radiusControl: 9
    readonly property real radiusWindow: 12
    readonly property real radiusDock: 24
    readonly property real veilOpacity: 0.62
    readonly property real sheetOpacity: 0.78
    readonly property int blurPanel: 32
    readonly property int fastMs: 180
    readonly property int slowMs: 320
    readonly property int breathMs: 2400
    readonly property string nasBase: "http://127.0.0.1:8092"
    readonly property string yaadBase: "http://127.0.0.1:8082"
    readonly property string dimaagBase: "http://127.0.0.1:8083"
}

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
    readonly property color sageFill: Qt.rgba(143 / 255, 163 / 255, 130 / 255, 0.2)
    readonly property color sageStroke: Qt.rgba(185 / 255, 201 / 255, 171 / 255, 0.45)
    readonly property color ink: "#141511"
    readonly property color inkMuted: "#8a8e87"
    readonly property color inkGhost: "#b0b8a6"
    readonly property color fail: "#c45c4a"
    readonly property color rule: "#14151118"
    readonly property int typeTitle: 17
    readonly property int typeSection: 13
    readonly property int typeBody: 16
    readonly property int typeMeta: 14
    readonly property int typeMark: 18
    readonly property int typeDisplay: 36
    readonly property real radiusControl: 10
    readonly property real radiusWindow: 16
    readonly property real radiusDock: 24
    readonly property real veilOpacity: 0.62
    readonly property real sheetOpacity: 0.78
    readonly property int blurPanel: 32
    readonly property int fastMs: 180
    readonly property int slowMs: 320
    readonly property int breathMs: 2400
    readonly property int widgetPollMs: 1000
    readonly property string nasBase: "http://127.0.0.1:8092"
    readonly property string yaadBase: "http://127.0.0.1:8082"
    readonly property string dimaagBase: "http://127.0.0.1:8083"
    readonly property string gharBase: "http://127.0.0.1:8084"
}

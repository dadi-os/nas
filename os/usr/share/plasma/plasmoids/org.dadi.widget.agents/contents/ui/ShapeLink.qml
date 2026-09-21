import QtQuick
import QtQuick.Shapes

Shape {
    id: root
    property real x1: 0
    property real y1: 0
    property real x2: 0
    property real y2: 0

    anchors.fill: parent
    preferredRendererType: Shape.CurveRenderer

    ShapePath {
        strokeWidth: 1
        strokeColor: "#14151126"
        fillColor: "transparent"
        capStyle: ShapePath.RoundCap
        PathMove { x: root.x1; y: root.y1 }
        PathLine { x: root.x2; y: root.y2 }
    }
}

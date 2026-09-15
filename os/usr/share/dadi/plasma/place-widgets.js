var desks = desktops();
if (desks.length < 1) {
    throw "no desktop containment";
}
var desktop = desks[0];

function findWidget(plugin) {
    var ids = desktop.widgetIds;
    var i;
    for (i = 0; i < ids.length; i++) {
        var widget = desktop.widgetById(ids[i]);
        if (widget && widget.type === plugin)
            return widget;
    }
    return null;
}

function placeAt(plugin, x, y, w, h) {
    var widget = findWidget(plugin);
    if (!widget) {
        desktop.addWidget(plugin, x, y, w, h);
        widget = findWidget(plugin);
    }
    if (!widget)
        throw "missing " + plugin;
    widget.geometry = new QRectF(x, y, w, h);
    return widget;
}

var geo = screenGeometry(desktop.screen);
var M = 64;
var G = 80;
var top = 64;
var dock = 96;
var row1h = 240;
var row2h = geo.height - 32 - top - row1h - G - dock;
if (row2h < 320)
    row2h = 320;
var col = Math.floor((geo.width - M * 2 - G) / 2);
var half = Math.floor((col - G) / 2);
var y1 = top + row1h + G;
var x1 = M + col + G;

var agents = placeAt("org.dadi.widget.agents", M, top, col, row1h);
var memory = placeAt("org.dadi.widget.memory", x1, top, col, row1h);
var timeline = placeAt("org.dadi.widget.timeline", M, y1, col, row2h);
var ghar = placeAt("org.dadi.widget.ghar", x1, y1, half, row2h);
var system = placeAt("org.dadi.widget.system", x1 + half + G, y1, half, row2h);

function entry(widget, x, y, w, h) {
    return "Applet-" + widget.id + ":" + x + "," + y + "," + w + "," + h + ",0";
}
var encoded = [
    entry(agents, M, top, col, row1h),
    entry(memory, x1, top, col, row1h),
    entry(timeline, M, y1, col, row2h),
    entry(ghar, x1, y1, half, row2h),
    entry(system, x1 + half + G, y1, half, row2h)
].join(";");
desktop.currentConfigGroup = [];
desktop.writeConfig("ItemGeometries-" + geo.width + "x" + geo.height, encoded);
desktop.writeConfig("ItemGeometriesHorizontal", encoded);

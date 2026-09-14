var desks = desktops();
if (desks.length < 1) {
    throw "no desktop containment";
}
var desktop = desks[0];

function place(plugin, x, y, w, h) {
    var ids = desktop.widgetIds;
    var i;
    for (i = 0; i < ids.length; i++) {
        var widget = desktop.widgetById(ids[i]);
        if (widget && widget.type === plugin) {
            widget.geometry = new QRectF(x, y, w, h);
            return;
        }
    }
    desktop.addWidget(plugin, x, y, w, h);
}

place("org.dadi.widget.agents", 48, 56, 860, 230);
place("org.dadi.widget.memory", 928, 56, 860, 230);
place("org.dadi.widget.timeline", 48, 306, 860, 500);
place("org.dadi.widget.system", 928, 306, 420, 500);

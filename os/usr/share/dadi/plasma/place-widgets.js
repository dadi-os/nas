/**
 * Place dadi crest widgets on a 4-column AppletsLayout grid (cell = 16px).
 * Row 0: agents (span 2), memory (span 2).
 * Row 1: timeline (span 2), ghar (span 1), system (span 1).
 * Invoked after the look-and-feel layout script via apply-desktop.sh.
 */
(function () {
    var desks = desktops();
    if (desks.length < 1)
        throw "no desktop containment";
    var desktop = desks[0];
    var geo = screenGeometry(desktop.screen);

    /** Sum of non-floating panel heights at a screen edge. */
    function panelStrut(location) {
        var ps = panels();
        var n = 0;
        var i;
        for (i = 0; i < ps.length; i++) {
            if (ps[i].location === location && ps[i].floating === false)
                n += ps[i].height;
        }
        return n;
    }

    /** Height of the first floating panel at location, or 0 when none. */
    function floatingHeight(location) {
        var ps = panels();
        var i;
        for (i = 0; i < ps.length; i++) {
            if (ps[i].location === location && ps[i].floating)
                return ps[i].height;
        }
        return 0;
    }

    var CELL = 16;
    var COLS = 4;
    var marginC = 1;
    var gapC = 2;

    /** Convert pixels to whole grid cells. */
    function cells(px) {
        return Math.floor(px / CELL);
    }

    /** Convert grid cells to pixels. */
    function px(c) {
        return c * CELL;
    }

    var topStrut = panelStrut("top");
    var bottomStrut = panelStrut("bottom");
    var containC = cells(geo.height - topStrut - bottomStrut);
    var dock = floatingHeight("bottom");
    var dockC = marginC;
    if (dock > 0)
        dockC = Math.max(marginC, Math.floor((dock + gapC * CELL) / CELL));

    var widthC = cells(geo.width);
    var colC = Math.floor((widthC - 2 * marginC - (COLS - 1) * gapC) / COLS);
    var innerC = containC - marginC - dockC;
    var row1C = Math.floor((innerC - gapC) / 3);
    var row2C = innerC - gapC - row1C;

    /** Left edge (px) of column index c. */
    function cellX(c) {
        return px(marginC + c * (colC + gapC));
    }

    /** Top edge (px) of row index r (0 = upper, 1 = lower). */
    function cellY(r) {
        if (r === 0)
            return px(marginC);
        return px(marginC + row1C + gapC);
    }

    /** Width (px) spanning `span` columns including gaps between them. */
    function cellW(span) {
        return px(span * colC + (span - 1) * gapC);
    }

    /** Height (px) of row index r. */
    function cellH(r) {
        if (r === 0)
            return px(row1C);
        return px(row2C);
    }

    /** Existing desktop widget of the given plasmoid plugin id, or null. */
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

    /**
     * Ensure plugin exists on the desktop and set its geometry.
     * Throws when Plasma cannot create or resolve the plasmoid.
     */
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

    var slots = [
        ["org.dadi.widget.agents", 0, 0, 2],
        ["org.dadi.widget.memory", 2, 0, 2],
        ["org.dadi.widget.timeline", 0, 1, 2],
        ["org.dadi.widget.ghar", 2, 1, 1],
        ["org.dadi.widget.system", 3, 1, 1]
    ];
    var encoded = [];
    var i;
    for (i = 0; i < slots.length; i++) {
        var plugin = slots[i][0];
        var x = cellX(slots[i][1]);
        var y = cellY(slots[i][2]);
        var w = cellW(slots[i][3]);
        var h = cellH(slots[i][2]);
        var widget = placeAt(plugin, x, y, w, h);
        encoded.push("Applet-" + widget.id + ":" + x + "," + y + "," + w + "," + h + ",0");
    }
    desktop.currentConfigGroup = [];
    desktop.writeConfig("ItemGeometries-" + geo.width + "x" + geo.height, encoded.join(";"));
    desktop.writeConfig("ItemGeometriesHorizontal", encoded.join(";"));
})();

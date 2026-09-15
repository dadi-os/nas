function clearPanels() {
    var ps = panels();
    var i;
    for (i = ps.length - 1; i >= 0; i--) {
        ps[i].remove();
    }
}

clearPanels();

var top = new Panel;
top.location = "top";
top.height = Math.max(32, Math.round(gridUnit * 1.5));
top.hiding = "none";
top.floating = false;
top.lengthMode = "fill";
top.opacity = "translucent";
top.addWidget("org.dadi.brand");
var kickoff = top.addWidget("org.kde.plasma.kickoff");
kickoff.currentConfigGroup = ["General"];
kickoff.writeConfig("icon", "dadi");
kickoff.writeConfig("lengthVisible", false);
top.addWidget("org.kde.plasma.appmenu");
top.addWidget("org.kde.plasma.panelspacer");
top.addWidget("org.kde.plasma.systemtray");
var clock = top.addWidget("org.kde.plasma.digitalclock");
clock.currentConfigGroup = ["Appearance"];
clock.writeConfig("showDate", false);
clock.writeConfig("use24hFormat", "2");
clock.writeConfig("fontFamily", "Noto Sans");

var dock = new Panel;
dock.location = "bottom";
dock.height = Math.max(56, 2 * Math.floor(gridUnit * 2.5 / 2));
dock.hiding = "none";
dock.floating = true;
dock.alignment = "center";
dock.lengthMode = "fit";
dock.opacity = "translucent";
dock.minimumLength = 420;
dock.maximumLength = 900;
var tasks = dock.addWidget("org.kde.plasma.icontasks");
tasks.currentConfigGroup = ["General"];
tasks.writeConfig(
    "launchers",
    "applications:org.kde.dolphin.desktop,applications:org.kde.konsole.desktop,applications:org.dadi.adddevice.desktop,applications:org.dadi.preferences.desktop"
);
tasks.writeConfig("max", "12");
tasks.writeConfig("iconSpacing", "2");

var desks = desktops();
if (desks.length < 1) {
    throw "no desktop containment";
}
var desktop = desks[0];
desktop.currentConfigGroup = ["General"];
desktop.writeConfig("showToolbox", false);
desktop.writeConfig("ToolBoxButtonState", "hidden");
desktop.writeConfig("iconSize", 1);
desktop.writeConfig("arrangement", 1);
desktop.writeConfig("sortMode", -1);

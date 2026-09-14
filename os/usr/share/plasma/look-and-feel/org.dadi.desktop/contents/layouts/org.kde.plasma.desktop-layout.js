var plasma = getApiVersion(1);

var layout = {
    "desktops": [
        {
            "applets": [],
            "config": {
                "/": {
                    "formfactor": "0",
                    "immutability": "0",
                    "lastScreen": "0",
                    "wallpaperplugin": "org.kde.image"
                },
                "/General": {
                    "ToolBoxButtonState": "hidden",
                    "showToolbox": "false",
                    "iconSize": "1",
                    "arrangement": "1",
                    "sortMode": "-1"
                },
                "/Wallpaper/org.kde.image/General": {
                    "Image": "file:///usr/share/wallpapers/Dadi/contents/images/1920x1080.png",
                    "FillMode": "2"
                }
            },
            "wallpaperPlugin": "org.kde.image"
        }
    ],
    "panels": [
        {
            "alignment": "center",
            "height": 32,
            "hiding": "normal",
            "lengthMode": "fill",
            "location": "top",
            "maximumLength": -1,
            "minimumLength": -1,
            "offset": 0,
            "opacity": "translucent",
            "applets": [
                {
                    "plugin": "org.dadi.brand",
                    "config": { "/": { "immutability": "1" } }
                },
                {
                    "plugin": "org.kde.plasma.kickoff",
                    "config": {
                        "/": { "immutability": "1" },
                        "/Configuration": { "PreloadWeight": "100" },
                        "/Configuration/General": {
                            "icon": "dadi",
                            "lengthVisible": "false",
                            "showActionButtonCaptions": "false"
                        },
                        "/Configuration/Shortcuts": { "global": "Alt+F1" }
                    }
                },
                {
                    "plugin": "org.kde.plasma.appmenu",
                    "config": { "/": { "immutability": "1" } }
                },
                {
                    "plugin": "org.kde.plasma.panelspacer",
                    "config": { "/": { "immutability": "1" } }
                },
                {
                    "plugin": "org.kde.plasma.systemtray",
                    "config": {
                        "/": { "immutability": "1" },
                        "/Configuration": { "PreloadWeight": "60" }
                    }
                },
                {
                    "plugin": "org.kde.plasma.digitalclock",
                    "config": {
                        "/": { "immutability": "1" },
                        "/Configuration/Appearance": {
                            "fontFamily": "Noto Sans",
                            "showDate": "false",
                            "use24hFormat": "2"
                        }
                    }
                }
            ],
            "config": {
                "/": { "immutability": "1" },
                "/ConfigDialog": {
                    "DialogHeight": "540",
                    "DialogWidth": "720"
                },
                "/Configuration/General": {
                    "lengthMode": "fill"
                }
            }
        },
        {
            "alignment": "center",
            "height": 56,
            "hiding": "normal",
            "lengthMode": "fit",
            "location": "bottom",
            "maximumLength": 900,
            "minimumLength": 420,
            "offset": 0,
            "opacity": "translucent",
            "floating": 1,
            "applets": [
                {
                    "plugin": "org.kde.plasma.taskmanager",
                    "config": {
                        "/": { "immutability": "1" },
                        "/Configuration/General": {
                            "launchers": "applications:org.kde.dolphin.desktop,applications:org.kde.konsole.desktop,applications:org.dadi.adddevice.desktop,applications:org.dadi.preferences.desktop",
                            "max": "12",
                            "showOnlyCurrentDesktop": "false",
                            "showOnlyCurrentActivity": "false",
                            "showOnlyCurrentScreen": "false",
                            "groupingStrategy": "0",
                            "iconSpacing": "2"
                        }
                    }
                }
            ],
            "config": {
                "/": { "immutability": "1" }
            }
        }
    ],
    "serializationFormatVersion": "1"
};

plasma.loadSerializedLayout(layout);

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

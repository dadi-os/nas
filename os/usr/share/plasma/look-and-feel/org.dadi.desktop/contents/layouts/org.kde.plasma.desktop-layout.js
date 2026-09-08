var plasma = getApiVersion(1);

var layout = {
    "desktops": [
        {
            "applets": [
                {
                    "plugin": "org.dadi.widget.system",
                    "geometry.x": 48,
                    "geometry.y": 48,
                    "geometry.width": 340,
                    "geometry.height": 210,
                    "config": { "/": { "immutability": "1" } }
                },
                {
                    "plugin": "org.dadi.widget.memory",
                    "geometry.x": 412,
                    "geometry.y": 48,
                    "geometry.width": 300,
                    "geometry.height": 180,
                    "config": { "/": { "immutability": "1" } }
                },
                {
                    "plugin": "org.dadi.widget.agents",
                    "geometry.x": 736,
                    "geometry.y": 48,
                    "geometry.width": 280,
                    "geometry.height": 170,
                    "config": { "/": { "immutability": "1" } }
                },
                {
                    "plugin": "org.dadi.widget.timeline",
                    "geometry.x": 48,
                    "geometry.y": 290,
                    "geometry.width": 340,
                    "geometry.height": 220,
                    "config": { "/": { "immutability": "1" } }
                },
                {
                    "plugin": "org.dadi.widget.logs",
                    "geometry.x": 412,
                    "geometry.y": 290,
                    "geometry.width": 380,
                    "geometry.height": 160,
                    "config": { "/": { "immutability": "1" } }
                }
            ],
            "config": {
                "/": {
                    "ItemGeometriesHorizontal": "",
                    "formfactor": "desktop",
                    "immutability": "1",
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
                    "Image": "file:///usr/share/wallpapers/DadiBloom/contents/images/1920x1080.png",
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
            "hiding": "autohide",
            "lengthMode": "fit",
            "location": "bottom",
            "maximumLength": 720,
            "minimumLength": 220,
            "offset": 0,
            "opacity": "translucent",
            "floating": 1,
            "applets": [
                {
                    "plugin": "org.kde.plasma.icontasks",
                    "config": {
                        "/": { "immutability": "1" },
                        "/Configuration/General": {
                            "launchers": "applications:org.kde.dolphin.desktop,applications:org.kde.konsole.desktop,applications:org.dadi.preferences.desktop",
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

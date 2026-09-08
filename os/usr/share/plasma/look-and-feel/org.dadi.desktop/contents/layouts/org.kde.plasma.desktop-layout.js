var plasma = getApiVersion(1);

var layout = {
    "desktops": [
        {
            "applets": [],
            "config": {
                "/": {
                    "ItemGeometriesHorizontal": "",
                    "formfactor": "desktop",
                    "immutability": "1",
                    "lastScreen": "0",
                    "wallpaperplugin": "org.kde.image"
                },
                "/ConfigDialog": {
                    "DialogHeight": "540",
                    "DialogWidth": "720"
                },
                "/Configuration": {
                    "PreloadWeight": "0"
                },
                "/General": {
                    "ToolBoxButtonState": "hidden",
                    "showToolbox": "false"
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
            "height": 30,
            "hiding": "normal",
            "lengthMode": "fill",
            "location": "top",
            "maximumLength": -1,
            "minimumLength": -1,
            "offset": 0,
            "opacity": "adaptive",
            "applets": [
                {
                    "plugin": "org.dadi.brand",
                    "config": {
                        "/": {
                            "immutability": "1"
                        }
                    }
                },
                {
                    "plugin": "org.kde.plasma.kickoff",
                    "config": {
                        "/": {
                            "immutability": "1"
                        },
                        "/Configuration": {
                            "PreloadWeight": "100"
                        },
                        "/Configuration/General": {
                            "icon": "dadi",
                            "lengthVisible": "false",
                            "favoritesDisplay": "1",
                            "primaryActions": "1",
                            "showActionButtonCaptions": "false",
                            "alphaSort": "false"
                        },
                        "/Configuration/Shortcuts": {
                            "global": "Alt+F1"
                        }
                    }
                },
                {
                    "plugin": "org.kde.plasma.appmenu",
                    "config": {
                        "/": {
                            "immutability": "1"
                        }
                    }
                },
                {
                    "plugin": "org.kde.plasma.panelspacer",
                    "config": {
                        "/": {
                            "immutability": "1"
                        }
                    }
                },
                {
                    "plugin": "org.kde.plasma.systemtray",
                    "config": {
                        "/": {
                            "immutability": "1"
                        },
                        "/Configuration": {
                            "PreloadWeight": "60"
                        }
                    }
                },
                {
                    "plugin": "org.kde.plasma.digitalclock",
                    "config": {
                        "/": {
                            "immutability": "1"
                        },
                        "/Configuration": {
                            "PreloadWeight": "55"
                        },
                        "/Configuration/Appearance": {
                            "fontFamily": "Noto Sans",
                            "showDate": "false",
                            "use24hFormat": "2"
                        }
                    }
                }
            ],
            "config": {
                "/": {
                    "immutability": "1"
                },
                "/ConfigDialog": {
                    "DialogHeight": "540",
                    "DialogWidth": "720"
                },
                "/Configuration": {
                    "PreloadWeight": "0"
                }
            }
        },
        {
            "alignment": "center",
            "height": 52,
            "hiding": "autohide",
            "lengthMode": "fit",
            "location": "bottom",
            "maximumLength": 800,
            "minimumLength": 200,
            "offset": 0,
            "opacity": "adaptive",
            "floating": 1,
            "applets": [
                {
                    "plugin": "org.kde.plasma.icontasks",
                    "config": {
                        "/": {
                            "immutability": "1"
                        },
                        "/Configuration/General": {
                            "launchers": "applications:org.kde.dolphin.desktop,applications:org.kde.konsole.desktop,applications:systemsettings.desktop",
                            "max": "12",
                            "showOnlyCurrentDesktop": "false",
                            "showOnlyCurrentActivity": "false",
                            "showOnlyCurrentScreen": "false",
                            "groupingStrategy": "0"
                        }
                    }
                }
            ],
            "config": {
                "/": {
                    "immutability": "1"
                },
                "/ConfigDialog": {
                    "DialogHeight": "540",
                    "DialogWidth": "720"
                }
            }
        }
    ],
    "serializationFormatVersion": "1"
};

plasma.loadSerializedLayout(layout);

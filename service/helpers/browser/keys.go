package browser

import (
	"context"
	"fmt"
	"strings"
)

// keyDef is how Chromium names a key: its key value, physical code and Windows virtual key code.
type keyDef struct {
	key  string
	code string
	vk   int
	text string
}

var namedKeys = map[string]keyDef{
	"enter": {"Enter", "Enter", 13, "\r"}, "return": {"Enter", "Enter", 13, "\r"},
	"escape": {"Escape", "Escape", 27, ""}, "esc": {"Escape", "Escape", 27, ""},
	"tab": {"Tab", "Tab", 9, ""}, "backspace": {"Backspace", "Backspace", 8, ""}, "delete": {"Delete", "Delete", 46, ""},
	"space": {" ", "Space", 32, " "}, " ": {" ", "Space", 32, " "},
	"arrowup": {"ArrowUp", "ArrowUp", 38, ""}, "arrowdown": {"ArrowDown", "ArrowDown", 40, ""},
	"arrowleft": {"ArrowLeft", "ArrowLeft", 37, ""}, "arrowright": {"ArrowRight", "ArrowRight", 39, ""},
	"home": {"Home", "Home", 36, ""}, "end": {"End", "End", 35, ""},
	"pageup": {"PageUp", "PageUp", 33, ""}, "pagedown": {"PageDown", "PageDown", 34, ""},
	"insert": {"Insert", "Insert", 45, ""},
}

// modifierBits are the Input.dispatchKeyEvent modifier flags.
var modifierBits = map[string]int{"alt": 1, "control": 2, "ctrl": 2, "meta": 4, "cmd": 4, "command": 4, "shift": 8}

// parseKey reads a key or chord in Playwright syntax ("Enter", "Control+Enter", "a",
// "Shift+ArrowDown") into the main key and the modifier flags.
func parseKey(spec string) (keyDef, int, error) {
	parts := strings.Split(spec, "+")
	if len(parts) > 1 && parts[len(parts)-1] == "" {
		parts = append(parts[:len(parts)-2], "+")
	}
	mods := 0
	for _, p := range parts[:len(parts)-1] {
		bit, ok := modifierBits[strings.ToLower(p)]
		if !ok {
			return keyDef{}, 0, fmt.Errorf("unknown modifier %q in key %q", p, spec)
		}
		mods |= bit
	}
	name := parts[len(parts)-1]
	if def, ok := namedKeys[strings.ToLower(name)]; ok {
		return def, mods, nil
	}
	if len(name) > 1 && strings.ToLower(name)[0] == 'f' {
		var n int
		if _, err := fmt.Sscanf(name[1:], "%d", &n); err == nil && n >= 1 && n <= 12 {
			return keyDef{key: strings.ToUpper(name[:1]) + name[1:], code: strings.ToUpper(name[:1]) + name[1:], vk: 111 + n}, mods, nil
		}
	}
	if r := []rune(name); len(r) == 1 {
		c := r[0]
		def := keyDef{key: name, text: name, vk: int(strings.ToUpper(name)[0])}
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			def.code = "Key" + strings.ToUpper(name)
		case c >= '0' && c <= '9':
			def.code = "Digit" + name
		}
		return def, mods, nil
	}
	return keyDef{}, 0, fmt.Errorf("unknown key %q", spec)
}

// modifierKeys are the keys pressed for each modifier name.
var modifierKeys = map[string]keyDef{
	"alt": {key: "Alt", code: "AltLeft", vk: 18}, "control": {key: "Control", code: "ControlLeft", vk: 17},
	"ctrl": {key: "Control", code: "ControlLeft", vk: 17}, "meta": {key: "Meta", code: "MetaLeft", vk: 91},
	"cmd": {key: "Meta", code: "MetaLeft", vk: 91}, "command": {key: "Meta", code: "MetaLeft", vk: 91},
	"shift": {key: "Shift", code: "ShiftLeft", vk: 16},
}

// press sends a key or chord to whatever has focus on the tab: modifiers down, the key down
// and up, modifiers up. Printable keys type their text unless Control, Alt or Meta is held.
func (t *Tab) press(ctx context.Context, spec string) error {
	def, mods, err := parseKey(spec)
	if err != nil {
		return err
	}
	send := func(params map[string]any) error {
		return t.sendOrDialog(ctx, "Input.dispatchKeyEvent", params)
	}
	var held []keyDef
	running := 0
	for _, name := range strings.Split(spec, "+")[:strings.Count(spec, "+")] {
		m := modifierKeys[strings.ToLower(name)]
		held = append(held, m)
		running |= modifierBits[strings.ToLower(name)]
		if err := send(map[string]any{"type": "rawKeyDown", "key": m.key, "code": m.code, "windowsVirtualKeyCode": m.vk, "modifiers": running}); err != nil {
			return err
		}
	}
	down := map[string]any{"type": "rawKeyDown", "key": def.key, "code": def.code, "windowsVirtualKeyCode": def.vk, "modifiers": mods}
	if def.text != "" && mods&(1|2|4) == 0 {
		down["type"], down["text"] = "keyDown", def.text
	}
	if err := send(down); err != nil {
		return err
	}
	if err := send(map[string]any{"type": "keyUp", "key": def.key, "code": def.code, "windowsVirtualKeyCode": def.vk, "modifiers": mods}); err != nil {
		return err
	}
	for i := len(held) - 1; i >= 0; i-- {
		running &^= modifierBits[strings.ToLower(strings.Split(spec, "+")[i])]
		if err := send(map[string]any{"type": "keyUp", "key": held[i].key, "code": held[i].code, "windowsVirtualKeyCode": held[i].vk, "modifiers": running}); err != nil {
			return err
		}
	}
	return nil
}

package browser

// jsLib is prepended to page scripts that search the DOM: deepEls lists every element of a
// root, open shadow roots included.
const jsLib = `
const deepEls = (root) => {
  const out = [];
  const walk = (node) => {
    for (const el of node.querySelectorAll("*")) {
      out.push(el);
      if (el.shadowRoot) walk(el.shadowRoot);
    }
  };
  walk(root);
  return out;
};
`

// snapshotScript is evaluated in each frame as (snapshotScript)(start, scope) with the first
// ref number to hand out and an optional scope (a ref, or text to find): walk the visible DOM
// through open shadow roots and slots, give interactive nodes a data-dadi-ref (keeping the
// ref a node already has, so refs stay stable between looks), and return the indented text
// outline with the next free ref number.
// CSS hiding drops a whole subtree, but an element with no box of its own (a 0x0 portal
// wrapper, as Trello puts its board and card dialog in) is only passed through, so the
// positioned content inside it still gets refs.
// A password input (type password, or a password autocomplete once a show-password toggle has
// made it text) reads filled instead of its value, so a filled password never reaches an outline.
const snapshotScript = `(start, scope) => {
  ` + jsLib + `
  const ROLE_CONTROLS = ["checkbox", "radio", "switch", "tab", "menuitem", "menuitemcheckbox",
    "menuitemradio", "option", "treeitem", "textbox", "searchbox", "combobox", "slider", "spinbutton"];

  let next = start;
  const lines = [];
  const MIN_TEXT = 2;

  /** True when el and everything inside it are invisible: hidden by CSS, or a 0x0 box that clips its overflow. */
  function isHidden(el) {
    if (!(el instanceof Element)) return true;
    const style = window.getComputedStyle(el);
    if (style.display === "none" || style.visibility === "hidden" || style.opacity === "0") {
      return true;
    }
    return (style.overflowX !== "visible" || style.overflowY !== "visible") && hasNoBox(el);
  }

  /** True when el lays out to 0x0; children positioned outside it can still be on screen. */
  function hasNoBox(el) {
    if (window.getComputedStyle(el).display === "contents") return false;
    const rect = el.getBoundingClientRect();
    return rect.width === 0 && rect.height === 0;
  }

  /** The nodes under node that walk visits: its open shadow root, a filled slot's assigned nodes, or its children. */
  function childrenOf(node, tag) {
    if (node.shadowRoot) return node.shadowRoot.childNodes;
    if (tag === "slot" && node.assignedNodes({ flatten: true }).length > 0) {
      return node.assignedNodes({ flatten: true });
    }
    return node.childNodes;
  }

  function assignRef(el) {
    const old = el.getAttribute("data-dadi-ref");
    if (old) return old;
    const ref = "e" + next++;
    el.setAttribute("data-dadi-ref", ref);
    return ref;
  }

  function indent(depth) {
    return "  ".repeat(depth);
  }

  function states(el) {
    return (el.disabled === true || el.getAttribute("aria-disabled") === "true" ? " disabled" : "")
      + (el.getAttribute("aria-checked") === "true" ? " checked" : "")
      + (el.getAttribute("aria-selected") === "true" ? " selected" : "");
  }

  function describeInput(el) {
    const type = (el.getAttribute("type") || "text").toLowerCase();
    const name = el.getAttribute("name") || el.getAttribute("aria-label") || el.id || "";
    const placeholder = el.getAttribute("placeholder") || "";
    const value = el.value || "";
    const secret = type === "password" || /password/i.test(el.getAttribute("autocomplete") || "");
    const checked = el.checked ? " checked" : "";
    return "input type=" + JSON.stringify(type)
      + (name ? " name=" + JSON.stringify(name) : "")
      + (placeholder ? " placeholder=" + JSON.stringify(placeholder) : "")
      + (value ? (secret ? " filled" : " value=" + JSON.stringify(value)) : "")
      + checked
      + states(el);
  }

  function walk(node, depth) {
    if (node.nodeType === Node.TEXT_NODE) {
      const text = (node.textContent || "").replace(/\s+/g, " ").trim();
      if (text.length >= MIN_TEXT) {
        const parent = node.parentElement;
        if (parent && !isHidden(parent) && !hasNoBox(parent) && !["SCRIPT", "STYLE", "NOSCRIPT"].includes(parent.tagName)) {
          lines.push(indent(depth) + "text " + JSON.stringify(text));
        }
      }
      return;
    }
    if (!(node instanceof Element)) return;
    if (["SCRIPT", "STYLE", "NOSCRIPT", "TEMPLATE"].includes(node.tagName)) return;
    if (isHidden(node)) return;

    const tag = node.tagName.toLowerCase();
    const role = (node.getAttribute("role") || "").toLowerCase();
    if (hasNoBox(node)) {
      for (const child of childrenOf(node, tag)) {
        walk(child, depth);
      }
      return;
    }
    let emitted = false;

    if (/^h[1-6]$/.test(tag) || tag === "main" || tag === "nav" || tag === "header"
        || tag === "footer" || tag === "aside" || tag === "section" || tag === "article"
        || tag === "dialog" || role === "dialog" || role === "alertdialog"
        || role === "main" || role === "navigation" || role === "banner") {
      const label = (node.getAttribute("aria-label") || node.innerText || "").replace(/\s+/g, " ").trim().slice(0, 120);
      lines.push(indent(depth) + tag + (label ? " " + JSON.stringify(label) : ""));
      emitted = true;
    }

    if (tag === "a" || role === "link") {
      const ref = assignRef(node);
      const href = node.getAttribute("href") || "";
      const text = (node.innerText || "").replace(/\s+/g, " ").trim().slice(0, 120);
      lines.push(indent(depth) + "link [" + ref + "]"
        + (text ? " " + JSON.stringify(text) : "")
        + (href ? " href=" + JSON.stringify(href) : ""));
      emitted = true;
    } else if (tag === "button" || role === "button") {
      const ref = assignRef(node);
      const text = (node.innerText || node.getAttribute("aria-label") || "").replace(/\s+/g, " ").trim().slice(0, 120);
      lines.push(indent(depth) + "button [" + ref + "]" + states(node) + (text ? " " + JSON.stringify(text) : ""));
      emitted = true;
    } else if (tag === "input") {
      const ref = assignRef(node);
      lines.push(indent(depth) + "[" + ref + "] " + describeInput(node));
      emitted = true;
    } else if (tag === "textarea") {
      const ref = assignRef(node);
      const name = node.getAttribute("name") || node.getAttribute("aria-label") || "";
      const value = node.value || "";
      lines.push(indent(depth) + "textarea [" + ref + "]" + states(node)
        + (name ? " name=" + JSON.stringify(name) : "")
        + (value ? " value=" + JSON.stringify(value.slice(0, 200)) : ""));
      emitted = true;
    } else if (tag === "select") {
      const ref = assignRef(node);
      const options = Array.from(node.options).map((o) => ({
        value: o.value,
        label: o.label || o.text,
        selected: o.selected,
      }));
      lines.push(indent(depth) + "select [" + ref + "]" + states(node) + " options=" + JSON.stringify(options));
      emitted = true;
    } else if (ROLE_CONTROLS.includes(role)
        || (node.isContentEditable && !(node.parentElement && node.parentElement.isContentEditable))) {
      const ref = assignRef(node);
      const kind = ROLE_CONTROLS.includes(role) ? role : "textbox";
      const text = (node.getAttribute("aria-label") || node.innerText || "").replace(/\s+/g, " ").trim().slice(0, 120);
      lines.push(indent(depth) + kind + " [" + ref + "]"
        + (node.isContentEditable ? " editable" : "")
        + states(node)
        + (text ? " " + JSON.stringify(text) : ""));
      emitted = true;
    } else if (tag === "img") {
      const alt = node.getAttribute("alt") || "";
      lines.push(indent(depth) + "img alt=" + JSON.stringify(alt));
      emitted = true;
    }

    const childDepth = emitted ? depth + 1 : depth;
    if (tag === "input" || tag === "textarea" || tag === "select" || tag === "img") {
      return;
    }
    for (const child of childrenOf(node, tag)) {
      walk(child, childDepth);
    }
  }

  const INTERACTIVE = "a,button,input,textarea,select,[role=button],[role=link],[role=checkbox],[role=textbox],[contenteditable]";

  /** The element a scope names: the ref's element, or the smallest region around the first element whose text contains scope. */
  function scopeRoot() {
    if (/^e[0-9]+$/.test(scope)) {
      return deepEls(document).find((e) => e.getAttribute("data-dadi-ref") === scope) || null;
    }
    const needle = scope.toLowerCase();
    let hit = null;
    for (const el of deepEls(document)) {
      if (isHidden(el)) continue;
      const own = Array.from(el.childNodes).filter((n) => n.nodeType === Node.TEXT_NODE).map((n) => n.textContent).join(" ");
      const label = (el.getAttribute("aria-label") || "") + " " + own;
      if (label.toLowerCase().includes(needle)) { hit = el; break; }
    }
    for (let up = 0; hit && up < 6 && hit.parentElement; up++) {
      if (hit.matches(INTERACTIVE) || hit.querySelector(INTERACTIVE)) break;
      hit = hit.parentElement;
    }
    return hit;
  }

  if (scope) {
    const root = scopeRoot();
    if (!root) return { lines: "", next, found: false };
    walk(root, 0);
    return { lines: lines.join("\n"), next, found: true };
  }
  if (document.body) {
    walk(document.body, 0);
  }
  return { lines: lines.join("\n"), next, found: true };
}`

// findScript is evaluated in each frame as (findScript)(query, start, limit): the visible
// elements, shadow roots included, whose text, label, placeholder, name, value, title, alt or
// role contains query, each given a ref. It returns the lines and the next free ref number.
const findScript = `(query, start, limit) => {
  ` + jsLib + `
  const needle = query.toLowerCase();
  let next = start;
  const out = [];
  const visible = (el) => {
    const s = getComputedStyle(el);
    if (s.display === "none" || s.visibility === "hidden") return false;
    return Array.from(el.getClientRects()).some((r) => r.width > 0 && r.height > 0);
  };
  const INTERACTIVE = /^(a|button|input|textarea|select|summary|label|option)$/;
  for (const el of deepEls(document)) {
    if (out.length >= limit) break;
    if (["SCRIPT", "STYLE", "NOSCRIPT", "TEMPLATE", "HTML", "HEAD", "BODY"].includes(el.tagName)) continue;
    const tag = el.tagName.toLowerCase();
    const role = (el.getAttribute("role") || "").toLowerCase();
    const own = Array.from(el.childNodes).filter((n) => n.nodeType === Node.TEXT_NODE).map((n) => n.textContent).join(" ").replace(/\s+/g, " ").trim();
    const attrs = ["aria-label", "placeholder", "name", "title", "alt", "id"].map((a) => el.getAttribute(a) || "");
    const value = el.tagName === "INPUT" && el.type === "password" ? "" : (el.value || "");
    const text = (INTERACTIVE.test(tag) || role) ? (el.innerText || "").replace(/\s+/g, " ").trim().slice(0, 200) : own;
    const haystack = [own, text, value, role, tag].concat(attrs).join(" \n ").toLowerCase();
    if (!haystack.includes(needle) || !visible(el)) continue;
    if (!INTERACTIVE.test(tag) && !role && !el.isContentEditable && own === "") continue;
    let ref = el.getAttribute("data-dadi-ref");
    if (!ref) { ref = "e" + next++; el.setAttribute("data-dadi-ref", ref); }
    const label = (el.getAttribute("aria-label") || text || el.getAttribute("placeholder") || el.getAttribute("name") || own).slice(0, 100);
    out.push("[" + ref + "] " + (role || tag) + (label ? " " + JSON.stringify(label) : "") + (el.disabled ? " disabled" : ""));
  }
  return { lines: out, next };
}`

// checkFn is called on an element (this) to report whether a click or fill can land on it:
// whether it is visible, enabled and writable, where its client rectangle is, and what
// covers its centre when something else would take the click.
const checkFn = `function() {
  const el = this;
  if (!el.isConnected) return { gone: true };
  const cs = getComputedStyle(el);
  const rect = Array.from(el.getClientRects()).find((r) => r.width > 0 && r.height > 0);
  const visible = !!rect && cs.visibility !== "hidden" && cs.visibility !== "collapse" && cs.display !== "none";
  const out = {
    visible,
    disabled: el.matches(":disabled") || el.getAttribute("aria-disabled") === "true",
    readonly: el.readOnly === true || el.getAttribute("aria-readonly") === "true",
    covered: "",
    rect: rect ? [rect.left, rect.top, rect.width, rect.height] : [0, 0, 0, 0],
  };
  if (!visible) return out;
  const cx = rect.left + rect.width / 2, cy = rect.top + rect.height / 2;
  if (cx < 0 || cy < 0 || cx > innerWidth || cy > innerHeight) { out.covered = "outside the viewport"; return out; }
  let hit = document.elementFromPoint(cx, cy);
  while (hit && hit.shadowRoot) {
    const inner = hit.shadowRoot.elementFromPoint(cx, cy);
    if (!inner || inner === hit) break;
    hit = inner;
  }
  const within = (outer, node) => { for (let n = node; n; n = n.parentNode || n.host) { if (n === outer) return true; } return false; };
  const label = hit && hit.closest ? hit.closest("label") : null;
  if (!hit || within(el, hit) || (label && label.control === el)) return out;
  out.covered = "<" + hit.tagName.toLowerCase() + (hit.id ? "#" + hit.id : "") + (hit.className && typeof hit.className === "string" ? "." + hit.className.trim().split(/\s+/).join(".") : "") + ">";
  return out;
}`

// prepareFillFn is called on an element (this): focus it and select its content, and say how
// to write into it: "text" (select then insert text), "editable" (a contenteditable region)
// or "set" (a value-typed input that takes no typing, set through its native setter).
const prepareFillFn = `function() {
  const el = this;
  el.focus();
  const type = (el.getAttribute("type") || "").toLowerCase();
  if (el.isContentEditable && !(el instanceof HTMLInputElement) && !(el instanceof HTMLTextAreaElement)) {
    const range = document.createRange();
    range.selectNodeContents(el);
    const sel = (el.getRootNode().getSelection ? el.getRootNode() : document).getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
    return "editable";
  }
  if (el instanceof HTMLInputElement && ["date", "time", "datetime-local", "month", "week", "color", "range"].includes(type)) return "set";
  if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) {
    el.select();
    return "text";
  }
  throw new Error("the element is not a text field or an editable region");
}`

// setValueFn sets a value-typed input through its native setter and fires input and change.
const setValueFn = `function(value) {
  const proto = Object.getPrototypeOf(this);
  const setter = Object.getOwnPropertyDescriptor(proto, "value").set;
  setter.call(this, value);
  this.dispatchEvent(new Event("input", { bubbles: true }));
  this.dispatchEvent(new Event("change", { bubbles: true }));
  return this.value;
}`

// readValueFn reports what a text field or editable region now holds, with passwords hidden.
const readValueFn = `function() {
  if (this instanceof HTMLInputElement && this.type === "password") return { secret: true, length: this.value.length };
  const v = this.isContentEditable && !("value" in this) ? this.innerText : this.value;
  return { secret: false, value: v };
}`

// selectFn picks the option of a select (this) whose value or label equals want.
const selectFn = `function(want) {
  if (!(this instanceof HTMLSelectElement)) throw new Error("the element is not a select");
  const opts = Array.from(this.options);
  const pick = opts.find((o) => o.value === want) || opts.find((o) => (o.label || o.text).trim() === want.trim());
  if (!pick) throw new Error("no option with value or label " + JSON.stringify(want) + "; options: " + JSON.stringify(opts.map((o) => o.label || o.text)));
  this.value = pick.value;
  this.dispatchEvent(new Event("input", { bubbles: true }));
  this.dispatchEvent(new Event("change", { bubbles: true }));
  return pick.label || pick.text;
}`

// checkedFn reports whether a checkbox, radio or ARIA checkable (this) is checked.
const checkedFn = `function() {
  if (this instanceof HTMLInputElement && (this.type === "checkbox" || this.type === "radio")) return this.checked;
  const a = this.getAttribute("aria-checked");
  if (a === null) throw new Error("the element is not a checkbox, radio or switch");
  return a === "true";
}`

// fileInfoFn lists the files a file input (this) holds.
const fileInfoFn = `function() {
  if (!(this instanceof HTMLInputElement) || this.type !== "file") return null;
  return { multiple: this.multiple, files: Array.from(this.files || []).map((f) => ({ name: f.name, size: f.size })) };
}`

// markFileInputsScript marks every file input of the frame (shadow roots included) as seen
// and returns how many there are.
const markFileInputsScript = `(function(seen) {
  ` + jsLib + `
  const inputs = deepEls(document).filter((e) => e instanceof HTMLInputElement && e.type === "file");
  inputs.forEach((e) => e.setAttribute("data-dadi-seen", seen));
  return inputs.length;
})`

// newFileInputsScript returns the file inputs in the frame not marked seen, as one object (a
// single new input), or the count.
const newFileInputsScript = `(function(seen) {
  ` + jsLib + `
  const inputs = deepEls(document).filter((e) => e instanceof HTMLInputElement && e.type === "file" && e.getAttribute("data-dadi-seen") !== seen);
  return inputs.length === 1 ? inputs[0] : inputs.length;
})`

// extractScript is evaluated in each frame as (extractScript)(what): the best table or list
// for the description what, as {score, kind, headers, rows} (rows of cell text).
const extractScript = `(what) => {
  ` + jsLib + `
  const words = what.toLowerCase().split(/[^a-z0-9]+/).filter((w) => w.length > 2);
  const clean = (s) => (s || "").replace(/\s+/g, " ").trim();
  const visible = (el) => {
    const s = getComputedStyle(el);
    return s.display !== "none" && s.visibility !== "hidden" && Array.from(el.getClientRects()).some((r) => r.width > 0 && r.height > 0);
  };
  const nameOf = (el) => {
    let text = (el.getAttribute("aria-label") || "") + " " + (el.caption ? el.caption.innerText : "");
    const labelled = el.getAttribute("aria-labelledby");
    if (labelled) text += " " + labelled.split(/\s+/).map((id) => (document.getElementById(id) || {}).innerText || "").join(" ");
    for (let n = el.previousElementSibling, i = 0; n && i < 2; n = n.previousElementSibling, i++) {
      if (/^H[1-6]$/.test(n.tagName) || n.tagName === "P" || n.tagName === "LABEL") text += " " + n.innerText;
    }
    const sect = el.closest("section,article,[role=region]");
    if (sect) { const h = sect.querySelector("h1,h2,h3,h4,h5,h6"); if (h) text += " " + h.innerText; }
    return text.toLowerCase();
  };
  const score = (el, rows) => {
    const name = nameOf(el);
    const hits = words.filter((w) => name.includes(w)).length;
    return hits * 1000 + rows;
  };
  const best = { score: -1 };
  for (const t of deepEls(document).filter((e) => e.tagName === "TABLE" || e.getAttribute("role") === "table" || e.getAttribute("role") === "grid")) {
    if (!visible(t)) continue;
    const trs = t.tagName === "TABLE" ? Array.from(t.rows) : Array.from(t.querySelectorAll("[role=row]"));
    const cells = (tr) => tr.tagName === "TR" ? Array.from(tr.cells) : Array.from(tr.querySelectorAll("[role=cell],[role=gridcell],[role=columnheader],[role=rowheader]"));
    const grid = trs.map((tr) => ({ head: cells(tr).length > 0 && cells(tr).every((c) => c.tagName === "TH" || c.getAttribute("role") === "columnheader"), cells: cells(tr).map((c) => clean(c.innerText)) })).filter((r) => r.cells.length > 0);
    if (grid.length === 0) continue;
    const headIdx = grid.findIndex((r) => r.head);
    const headers = headIdx >= 0 ? grid[headIdx].cells : [];
    const rows = grid.filter((r, i) => i !== headIdx).map((r) => r.cells);
    const s = score(t, rows.length);
    if (s > best.score) Object.assign(best, { score: s, kind: "table", headers, rows });
  }
  for (const l of deepEls(document).filter((e) => e.tagName === "UL" || e.tagName === "OL" || e.getAttribute("role") === "list")) {
    if (!visible(l)) continue;
    const items = Array.from(l.children).filter((c) => c.tagName === "LI" || c.getAttribute("role") === "listitem").map((c) => clean(c.innerText)).filter((s) => s);
    if (items.length < 2) continue;
    const s = score(l, items.length) - 1;
    if (s > best.score) Object.assign(best, { score: s, kind: "list", headers: [], rows: items.map((i) => [i]) });
  }
  if (best.score < 0) return { score: -1 };
  return best;
}`

// textScript is evaluated in each frame as (textScript)(): the visible text of the page's main region.
const textScript = `() => {
  const main = document.querySelector("main,[role=main]") || document.body;
  return main ? main.innerText : "";
}`

// nextControlScript is evaluated in each frame as (nextControlScript)(label): the visible,
// enabled "next page" or "load more" control, or 0. label is its text when the helper named it.
const nextControlScript = `(label) => {
  ` + jsLib + `
  const want = label ? new RegExp(label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "i") : /^\s*(next|next page|load more|show more|more|older|>|›|»)\s*$/i;
  for (const el of deepEls(document)) {
    if (!(el.matches("a,button,[role=button],[role=link],input[type=button],input[type=submit]"))) continue;
    const text = (el.getAttribute("aria-label") || el.innerText || el.value || "").trim();
    if (!want.test(text)) continue;
    if (el.matches(":disabled") || el.getAttribute("aria-disabled") === "true" || el.closest(".disabled")) continue;
    if (!Array.from(el.getClientRects()).some((r) => r.width > 0 && r.height > 0)) continue;
    let ref = el.getAttribute("data-dadi-ref");
    if (!ref) { ref = "n" + Math.random().toString(36).slice(2, 8); el.setAttribute("data-dadi-ref", ref); }
    return ref;
  }
  return 0;
}`

// loginFieldsScript is evaluated in each frame as (loginFieldsScript)(start): refs for the
// visible username and password inputs, the remember-this-device checkbox and the submit
// button, and whether the page asks for a one-time code.
const loginFieldsScript = `(start) => {
  ` + jsLib + `
  let next = start;
  const visible = (el) => {
    const s = getComputedStyle(el);
    return s.display !== "none" && s.visibility !== "hidden" && Array.from(el.getClientRects()).some((r) => r.width > 0 && r.height > 0);
  };
  const refOf = (el) => {
    if (!el) return "";
    let ref = el.getAttribute("data-dadi-ref");
    if (!ref) { ref = "e" + next++; el.setAttribute("data-dadi-ref", ref); }
    return ref;
  };
  const inputs = deepEls(document).filter((e) => e instanceof HTMLInputElement && visible(e) && !e.disabled);
  const password = inputs.find((e) => e.type === "password");
  const hint = (e) => ((e.name || "") + " " + (e.id || "") + " " + (e.autocomplete || "") + " " + (e.getAttribute("aria-label") || "") + " " + (e.placeholder || "")).toLowerCase();
  const user = inputs.find((e) => ["text", "email", "tel", ""].includes(e.type) && /user|email|login|account|id\b|name/.test(hint(e)) && !/code|otp|token/.test(hint(e)))
    || inputs.find((e) => ["text", "email", "tel", ""].includes(e.type) && !/code|otp|token|search/.test(hint(e)));
  const code = inputs.find((e) => /one-time-code/.test(e.autocomplete || "") || /otp|verification|passcode|security.?code|mfa|2fa|token|\bcode\b/.test(hint(e)));
  const remember = deepEls(document).find((e) => e instanceof HTMLInputElement && e.type === "checkbox" && visible(e)
    && /remember|trust|stay signed|keep me/i.test(((e.labels && e.labels[0] ? e.labels[0].innerText : "") + " " + hint(e))));
  return {
    username: refOf(user), password: refOf(password), code: refOf(code), remember: remember ? { ref: refOf(remember), checked: remember.checked } : null,
    next,
  };
}`

// oktaKeyScript is evaluated in each frame as (oktaKeyScript)(start): the ref of the Select
// control of the "Security Key or Biometric Authenticator" row on an Okta verify list, or "".
const oktaKeyScript = `(start) => {
  ` + jsLib + `
  const target = deepEls(document).filter((e) => /^security key or biometric authenticator$/i.test((e.innerText || "").trim().split("\n")[0].trim()) && e.children.length < 3).pop();
  if (!target) return { ref: "", next: start };
  for (let row = target, up = 0; row && up < 6; row = row.parentElement, up++) {
    const btn = row.querySelector("a,button,[role=button],input[type=submit]");
    if (btn && btn !== target) {
      let ref = btn.getAttribute("data-dadi-ref");
      if (!ref) { ref = "e" + start; btn.setAttribute("data-dadi-ref", ref); return { ref, next: start + 1 }; }
      return { ref, next: start };
    }
  }
  return { ref: "", next: start };
}`

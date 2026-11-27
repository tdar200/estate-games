// Update a container to match an HTML string while keeping the DOM nodes that are still there.
//
// Every screen here is rendered as an HTML string from the state. Writing that string with
// innerHTML on every update replaced every node: entrance animations (a dialog's lift-in, the
// status pill, a banner) replayed, endless ones (the waiting ring, the busy dots) restarted, token
// SVGs and the QR image were rebuilt, and a modal open over the game blinked out and faded back in
// whenever anyone else moved. morph() walks the old and new trees side by side instead and changes
// only attributes and text that differ; nodes are created only where something new appears.
//
//   morph(root, html)            root's children become html (no-op when html is unchanged)
//   paint(root, html, key)       morph while `key` stays the same; a new key starts fresh (a
//                                different dialog gets its entrance animation)
//
// Matching: siblings pair up in order when their tag (and id, and data-key, when set) agree;
// elements with data-key also pair up by key wherever they moved (plates when the order changes).
// A data-key that changes makes a new element, which is how a banner with new words animates in.
// Kept as they are: the value of a focused input, classes starting with "js-" or "is-flash" (added
// by scripts for a moment), and the children of an element whose `__morphHold` is set (a number
// that is counting up).

const last = new WeakMap();

export function morph(root, html) {
  if (!root) return false;
  const text = String(html ?? "");
  if (last.get(root) === text) return false;
  last.set(root, text);
  const template = root.ownerDocument.createElement("template");
  template.innerHTML = text;
  morphChildren(root, template.content);
  return true;
}

export function paint(root, html, key = "") {
  if (!root) return false;
  if ((root.dataset.paintKey ?? "") !== String(key)) {
    root.dataset.paintKey = String(key);
    const text = String(html ?? "");
    root.innerHTML = text;
    last.set(root, text);
    return true;
  }
  return morph(root, html);
}

// Forget what was written, so the next morph() compares the real nodes again (after a script
// changed the container's children directly).
export function forget(root) {
  if (root) last.delete(root);
}

const KEEP_CLASS = /^(js-|is-flash)/;

function keyOf(node) {
  return node.nodeType === 1 ? node.getAttribute("data-key") : null;
}

function compatible(a, b) {
  if (a.nodeType !== b.nodeType) return false;
  if (a.nodeType !== 1) return true;
  if (a.namespaceURI !== b.namespaceURI || a.localName !== b.localName) return false;
  if ((a.id || b.id) && a.id !== b.id) return false;
  if (keyOf(a) !== keyOf(b)) return false;
  if (a.localName === "input" && a.type !== b.type) return false;
  return true;
}

function morphChildren(from, to) {
  const keyed = new Map();
  for (const node of from.childNodes) {
    const key = keyOf(node);
    if (key !== null && !keyed.has(key)) keyed.set(key, node);
  }
  let cursor = from.firstChild;
  for (const next of [...to.childNodes]) {
    let match = null;
    const key = keyOf(next);
    if (key !== null) {
      const found = keyed.get(key);
      if (found && found.parentNode === from && compatible(found, next)) match = found;
    } else if (cursor) {
      // The same position, or a little further on when something before it went away.
      for (let probe = cursor, steps = 0; probe && steps < 4; probe = probe.nextSibling, steps += 1) {
        if (keyOf(probe) === null && compatible(probe, next)) {
          match = probe;
          break;
        }
      }
    }
    if (match) {
      if (match === cursor) cursor = cursor.nextSibling;
      else from.insertBefore(match, cursor);
      if (key !== null) keyed.delete(key);
      update(match, next);
    } else {
      from.insertBefore(next, cursor);
    }
  }
  while (cursor) {
    const gone = cursor;
    cursor = cursor.nextSibling;
    from.removeChild(gone);
  }
}

function update(node, next) {
  if (node.nodeType !== 1) {
    if (node.nodeValue !== next.nodeValue) node.nodeValue = next.nodeValue;
    return;
  }
  syncAttributes(node, next);
  if (node.__morphHold) return;
  if (node.localName === "textarea") {
    if (node.ownerDocument.activeElement !== node && node.value !== next.value) node.value = next.value;
    return;
  }
  morphChildren(node, next);
}

function syncAttributes(node, next) {
  const focused = node.ownerDocument.activeElement === node;
  for (const attr of [...next.attributes]) {
    const name = attr.name;
    if (name === "class") {
      const keep = [...node.classList].filter((cls) => KEEP_CLASS.test(cls) && !next.classList.contains(cls));
      const value = keep.length ? `${attr.value} ${keep.join(" ")}`.trim() : attr.value;
      if (node.getAttribute("class") !== value) node.setAttribute("class", value);
      continue;
    }
    if (node.getAttributeNS(attr.namespaceURI, attr.localName) !== attr.value) {
      if (attr.namespaceURI) node.setAttributeNS(attr.namespaceURI, name, attr.value);
      else node.setAttribute(name, attr.value);
      if (name === "value" && node.localName === "input" && !focused) node.value = attr.value;
    }
  }
  for (const attr of [...node.attributes]) {
    if (next.hasAttributeNS(attr.namespaceURI, attr.localName)) continue;
    if (attr.name === "class") {
      const keep = [...node.classList].filter((cls) => KEEP_CLASS.test(cls));
      if (keep.length) node.setAttribute("class", keep.join(" "));
      else node.removeAttribute("class");
      continue;
    }
    node.removeAttributeNS(attr.namespaceURI, attr.localName);
  }
  if (node.localName === "input") {
    const checked = next.hasAttribute("checked");
    if ((node.type === "checkbox" || node.type === "radio") && node.checked !== checked) node.checked = checked;
    // An input the app rewrote (a code that was cleared) follows, unless someone is typing in it.
    if (!focused && node.value !== (next.getAttribute("value") ?? "") && node.type !== "checkbox" && node.type !== "radio" && node.type !== "range") node.value = next.getAttribute("value") ?? "";
    if (node.type === "range" && !focused && next.hasAttribute("value") && node.value !== next.getAttribute("value")) node.value = next.getAttribute("value");
  }
}

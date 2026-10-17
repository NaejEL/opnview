/*
 * A minimal document for the front's tests, run in the interpreter beside the front's own files.
 *
 * IT IS NOT A BROWSER AND DOES NOT PRETEND TO BE ONE. It holds exactly what the front's core uses --
 * elements and text nodes in a tree, attributes, properties of fields, events that bubble, the
 * focus, a selection, a location and a history -- so the reconciler and the views can be driven and
 * their output read. Layout is absent: an element measures nothing.
 */
(function (root) {
  "use strict";

  var documentListeners = {};

  function Node(type) {
    this.nodeType = type;
    this.childNodes = [];
    this.parentNode = null;
    this.ownerDocument = root.document;
  }
  Object.defineProperty(Node.prototype, "firstChild", { get: function () { return this.childNodes[0] || null; } });
  Object.defineProperty(Node.prototype, "lastChild", {
    get: function () { return this.childNodes[this.childNodes.length - 1] || null; }
  });
  Object.defineProperty(Node.prototype, "nextSibling", {
    get: function () {
      if (!this.parentNode) { return null; }
      var siblings = this.parentNode.childNodes;
      return siblings[siblings.indexOf(this) + 1] || null;
    }
  });
  Object.defineProperty(Node.prototype, "textContent", {
    get: function () {
      if (this.nodeType === 3) { return this.nodeValue; }
      return this.childNodes.map(function (child) { return child.textContent; }).join("");
    }
  });
  Node.prototype.contains = function (other) {
    for (var node = other; node; node = node.parentNode) { if (node === this) { return true; } }
    return false;
  };
  Node.prototype.appendChild = function (child) { return this.insertBefore(child, null); };
  Node.prototype.insertBefore = function (child, reference) {
    if (child.parentNode) { child.parentNode.removeChild(child); }
    var index = reference ? this.childNodes.indexOf(reference) : -1;
    if (index < 0) { this.childNodes.push(child); } else { this.childNodes.splice(index, 0, child); }
    child.parentNode = this;
    return child;
  };
  Node.prototype.removeChild = function (child) {
    var index = this.childNodes.indexOf(child);
    if (index < 0) { throw new Error("dom: removeChild of a node that is not a child"); }
    this.childNodes.splice(index, 1);
    child.parentNode = null;
    root.__dom.removed += 1;
    if (root.document.activeElement && child.contains(root.document.activeElement)) {
      root.document.activeElement = root.document.body;
    }
    return child;
  };

  function Text(value) {
    Node.call(this, 3);
    this.nodeValue = String(value);
    this.nodeName = "#text";
  }
  Text.prototype = Object.create(Node.prototype);

  function Element(tag, namespace) {
    Node.call(this, 1);
    this.tagName = String(tag).toUpperCase();
    this.nodeName = this.tagName;
    this.namespaceURI = namespace || null;
    this.attributes = {};
    this.listeners = {};
    this.value = "";
    this.checked = false;
    this.selected = false;
    this.disabled = false;
    var style = {};
    this.style = { setProperty: function (name, value) { style[name] = value; }, getPropertyValue: function (name) {
      return style[name] || ""; } };
  }
  Element.prototype = Object.create(Node.prototype);
  Element.prototype.setAttribute = function (name, value) {
    this.attributes[name] = String(value);
    if (name === "id") { this.id = String(value); }
  };
  Element.prototype.getAttribute = function (name) {
    return Object.prototype.hasOwnProperty.call(this.attributes, name) ? this.attributes[name] : null;
  };
  Element.prototype.hasAttribute = function (name) { return Object.prototype.hasOwnProperty.call(this.attributes, name); };
  Element.prototype.removeAttribute = function (name) { delete this.attributes[name]; };
  Element.prototype.addEventListener = function (type, handler) {
    (this.listeners[type] || (this.listeners[type] = [])).push(handler);
  };
  Element.prototype.removeEventListener = function (type, handler) {
    var list = this.listeners[type] || [];
    var index = list.indexOf(handler);
    if (index >= 0) { list.splice(index, 1); }
  };
  Element.prototype.dispatchEvent = function (event) {
    event.target = event.target || this;
    var path = [];
    for (var node = this; node; node = node.parentNode) { path.push(node); }
    for (var i = 0; i < path.length && !event.stopped; i++) {
      event.currentTarget = path[i];
      (path[i].listeners[event.type] || []).slice().forEach(function (handler) { handler(event); });
      if (!event.bubbles) { break; }
    }
    if (event.bubbles && !event.stopped) {
      event.currentTarget = root.document;
      (documentListeners[event.type] || []).slice().forEach(function (handler) { handler(event); });
    }
    if (!event.defaultPrevented && event.type === "click") {
      // A click on a submit button submits its form, as a browser does.
      var form = this;
      var isSubmit = this.tagName === "BUTTON" && (this.getAttribute("type") || "submit") === "submit";
      if (isSubmit && !this.disabled) {
        while (form && form.tagName !== "FORM") { form = form.parentNode; }
        if (form) { form.dispatchEvent(makeEvent("submit", { bubbles: true })); }
      }
    }
    return !event.defaultPrevented;
  };
  Element.prototype.focus = function () {
    var previous = root.document.activeElement;
    if (previous && previous !== this && previous.dispatchEvent) {
      previous.dispatchEvent(makeEvent("focusout", { bubbles: true }));
    }
    root.document.activeElement = this;
  };
  Element.prototype.blur = function () {
    if (root.document.activeElement === this) {
      root.document.activeElement = root.document.body;
      this.dispatchEvent(makeEvent("focusout", { bubbles: true }));
    }
  };
  Element.prototype.querySelectorAll = function (selector) { return query(this, selector); };
  Element.prototype.querySelector = function (selector) { return query(this, selector)[0] || null; };
  Object.defineProperty(Element.prototype, "offsetHeight", { get: function () { return 0; } });

  function makeEvent(type, options) {
    var settings = options || {};
    return {
      type: type, bubbles: settings.bubbles !== false, target: settings.target || null, currentTarget: null,
      defaultPrevented: false, stopped: false, button: 0, key: settings.key,
      preventDefault: function () { this.defaultPrevented = true; },
      stopPropagation: function () { this.stopped = true; }
    };
  }

  /* Selectors: compound parts -- a tag, #id, .class, [name] or [name="value"] -- joined by spaces
   * for descendants. That is every selector the tests use. */
  function compound(text) {
    var parts = { tag: null, id: null, classes: [], attributes: [] };
    var pattern = /([a-zA-Z][a-zA-Z0-9-]*)|#([a-zA-Z0-9_-]+)|\.([a-zA-Z0-9_-]+)|\[([a-zA-Z0-9_-]+)(?:="([^"]*)")?\]/g;
    var found;
    while ((found = pattern.exec(text))) {
      if (found[1]) { parts.tag = found[1].toUpperCase(); }
      if (found[2]) { parts.id = found[2]; }
      if (found[3]) { parts.classes.push(found[3]); }
      if (found[4]) { parts.attributes.push([found[4], found[5]]); }
    }
    return parts;
  }

  function matches(element, parts) {
    if (element.nodeType !== 1) { return false; }
    if (parts.tag && element.tagName !== parts.tag) { return false; }
    if (parts.id && element.getAttribute("id") !== parts.id) { return false; }
    var classes = (element.getAttribute("class") || "").split(/\s+/);
    for (var i = 0; i < parts.classes.length; i++) { if (classes.indexOf(parts.classes[i]) < 0) { return false; } }
    for (var j = 0; j < parts.attributes.length; j++) {
      var name = parts.attributes[j][0], value = parts.attributes[j][1];
      if (!element.hasAttribute(name)) { return false; }
      if (value !== undefined && element.getAttribute(name) !== value) { return false; }
    }
    return true;
  }

  function descendants(node, into) {
    node.childNodes.forEach(function (child) {
      if (child.nodeType === 1) { into.push(child); descendants(child, into); }
    });
    return into;
  }

  function query(scope, selector) {
    var steps = selector.trim().split(/\s+/).map(compound);
    var current = [scope];
    steps.forEach(function (step) {
      var next = [];
      current.forEach(function (node) {
        descendants(node, []).forEach(function (candidate) {
          if (matches(candidate, step) && next.indexOf(candidate) < 0) { next.push(candidate); }
        });
      });
      current = next;
    });
    return current;
  }

  var documentElement = new Element("html");
  var body = new Element("body");
  root.__dom = { created: 0, removed: 0 };
  root.document = {
    nodeType: 9,
    readyState: "complete",
    visibilityState: "visible",
    title: "",
    documentElement: documentElement,
    body: body,
    activeElement: null,
    createElement: function (tag) { root.__dom.created += 1; return new Element(tag); },
    createElementNS: function (namespace, tag) { root.__dom.created += 1; return new Element(tag, namespace); },
    createTextNode: function (value) { root.__dom.created += 1; return new Text(value); },
    getElementById: function (id) {
      return query(documentElement, "#" + id)[0] || null;
    },
    addEventListener: function (type, handler) { (documentListeners[type] || (documentListeners[type] = [])).push(handler); },
    removeEventListener: function () {},
    querySelectorAll: function (selector) { return query(documentElement, selector); },
    querySelector: function (selector) { return query(documentElement, selector)[0] || null; }
  };
  // Nodes made before the document existed take it now.
  documentElement.ownerDocument = root.document;
  body.ownerDocument = root.document;
  documentElement.appendChild(body);
  root.document.activeElement = body;

  root.__makeEvent = makeEvent;
  root.__selection = { rangeCount: 0, isCollapsed: true, anchorNode: null };
  root.getSelection = function () { return root.__selection; };
})(this);

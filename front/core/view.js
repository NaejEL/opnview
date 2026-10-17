/*
 * opnview front -- view trees, and the one reconciler that applies them to the page (A6).
 *
 * A VIEW IS A TREE OF KEYED NODES: an element name, its attributes, its properties (a field's value,
 * a box's checked state), its event handlers and its children; a string is a text node. A view
 * function returns one, and the reconciler makes the page match it:
 *
 *   - children are matched by key -- an explicit `key`, else the element name and position -- and a
 *     node is created or removed only when its key appears or disappears;
 *   - attributes and text are updated in place, so a node a reader is looking at keeps its identity,
 *     its scroll position and whatever the browser holds on it;
 *   - ON A REFRESH -- data arriving that the reader did not ask for -- a subtree holding the focus on
 *     a field, a non-collapsed selection, a form holding unapplied values (data-dirty="true") or a
 *     region that manages itself (data-reconcile="manual") is left untouched, and patched once the
 *     focus or the selection leaves it. A render answering the reader's own action patches it.
 *
 * NOTHING IS PARSED AS MARKUP. There is no innerHTML anywhere in the front; an attribute whose name
 * begins with "on", or a style attribute, is refused when the tree is built, so a handler is always a
 * function the reconciler attaches and a colour is always a class of the stylesheet.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  var SVG_NAMESPACE = "http://www.w3.org/2000/svg";
  var PROPERTIES = { value: true, checked: true, selected: true, indeterminate: true };

  /* Builds one element node. options holds the attributes, and three members of its own: `key`,
   * `on` (event name to handler) and `props` (properties set on the element rather than written as
   * attributes). children is a node, a string, or an array of them, nested arrays flattened and
   * null, undefined and false left out. */
  function h(tag, options, children) {
    var node = { tag: tag, key: undefined, attrs: {}, props: {}, on: {}, children: [] };
    var given = options || {};
    Object.keys(given).forEach(function (name) {
      var value = given[name];
      if (name === "key") { node.key = value; return; }
      if (name === "on") { node.on = value || {}; return; }
      if (name === "props") { node.props = value || {}; return; }
      if (/^on/i.test(name) || name.toLowerCase() === "style") {
        throw new Error("view: the attribute " + name + " is refused: a handler is attached by the reconciler " +
          "and a colour is a class");
      }
      if (PROPERTIES[name]) { node.props[name] = value; return; }
      node.attrs[name] = value;
    });
    node.children = flatten(children === undefined ? [] : children);
    return node;
  }

  function flatten(children) {
    var flat = [];
    (function walk(item) {
      if (item === null || item === undefined || item === false) { return; }
      if (Array.isArray(item)) { item.forEach(walk); return; }
      if (typeof item === "number") { flat.push(String(item)); return; }
      flat.push(item);
    })(children);
    return flat;
  }

  function keyOf(vnode, index) {
    if (typeof vnode === "string") { return "#text:" + index; }
    return vnode.key !== undefined && vnode.key !== null ? "key:" + vnode.key : vnode.tag + ":" + index;
  }

  function compatible(node, vnode) {
    if (typeof vnode === "string") { return node.nodeType === 3; }
    return node.nodeType === 1 && node.__otag === vnode.tag;
  }

  var deferred = false;

  function isEditable(element) {
    if (!element || element.nodeType !== 1) { return false; }
    var tag = String(element.tagName || element.nodeName).toUpperCase();
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") { return true; }
    return element.getAttribute && element.getAttribute("contenteditable") === "true";
  }

  /* Whether a refresh must leave this element alone: the field holding the focus, the smallest
   * element holding the whole of a non-collapsed selection, a form holding unapplied values, and a
   * region that manages itself. Their ancestors are patched around them, which keeps every node in
   * place, so neither the focus nor the selection moves. */
  function protectedElement(element) {
    var document = element.ownerDocument;
    if (element.getAttribute("data-reconcile") === "manual" || element.getAttribute("data-dirty") === "true") {
      return true;
    }
    if (document && element === document.activeElement && isEditable(element)) { return true; }
    var selection = root.getSelection ? root.getSelection() : null;
    if (selection && selection.rangeCount > 0 && !selection.isCollapsed && selection.anchorNode) {
      var focusNode = selection.focusNode || selection.anchorNode;
      if (element.contains(selection.anchorNode) && element.contains(focusNode)) {
        for (var child = element.firstChild; child; child = child.nextSibling) {
          if (child.nodeType === 1 && child.contains(selection.anchorNode) && child.contains(focusNode)) {
            return false;
          }
        }
        return true;
      }
    }
    return false;
  }

  function createNode(document, vnode, inSvg) {
    if (typeof vnode === "string") {
      var text = document.createTextNode(vnode);
      return text;
    }
    var svg = inSvg || vnode.tag === "svg";
    var element = svg ? document.createElementNS(SVG_NAMESPACE, vnode.tag) : document.createElement(vnode.tag);
    element.__otag = vnode.tag;
    element.__oattrs = {};
    element.__oon = {};
    patchElement(element, vnode, { refresh: false }, svg);
    return element;
  }

  function patchAttributes(element, attrs) {
    var previous = element.__oattrs || {};
    Object.keys(previous).forEach(function (name) {
      if (!Object.prototype.hasOwnProperty.call(attrs, name) || attrs[name] === null || attrs[name] === undefined ||
          attrs[name] === false) {
        element.removeAttribute(name);
      }
    });
    var current = {};
    Object.keys(attrs).forEach(function (name) {
      var value = attrs[name];
      if (value === null || value === undefined || value === false) { return; }
      var text = value === true ? "" : String(value);
      current[name] = text;
      if (previous[name] !== text || element.getAttribute(name) !== text) { element.setAttribute(name, text); }
    });
    element.__oattrs = current;
  }

  function patchEvents(element, handlers) {
    var registered = element.__oon || (element.__oon = {});
    Object.keys(registered).forEach(function (type) {
      if (!Object.prototype.hasOwnProperty.call(handlers, type)) { registered[type] = null; }
    });
    Object.keys(handlers).forEach(function (type) {
      if (!Object.prototype.hasOwnProperty.call(registered, type)) {
        element.addEventListener(type, function (event) {
          var handler = element.__oon[type];
          if (handler) { handler(event); }
        });
      }
      registered[type] = handlers[type];
    });
  }

  function patchProperties(element, props) {
    var document = element.ownerDocument;
    Object.keys(props).forEach(function (name) {
      var value = props[name];
      // The field the reader is typing in is theirs: its value is never written over.
      if (name === "value" && document && document.activeElement === element) { return; }
      if (element[name] !== value) { element[name] = value; }
    });
  }

  function patchElement(element, vnode, context, inSvg) {
    patchAttributes(element, vnode.attrs);
    patchEvents(element, vnode.on);
    patchChildren(element, vnode.children, context, inSvg || vnode.tag === "svg");
    patchProperties(element, vnode.props);
  }

  function patchNode(node, vnode, context, inSvg) {
    if (typeof vnode === "string") {
      if (node.nodeValue !== vnode) { node.nodeValue = vnode; }
      return;
    }
    if (context.refresh && protectedElement(node)) {
      deferred = true;
      return;
    }
    patchElement(node, vnode, context, inSvg);
  }

  function patchChildren(parent, vchildren, context, inSvg) {
    var document = parent.ownerDocument;
    var byKey = {};
    /* The children as they stand, read once. */
    var existing = Array.prototype.slice.call(parent.childNodes);
    existing.forEach(function (node) {
      if (node.__okey !== undefined && !Object.prototype.hasOwnProperty.call(byKey, node.__okey)) {
        byKey[node.__okey] = node;
      }
    });
    var kept = [];
    vchildren.forEach(function (vnode, index) {
      var key = keyOf(vnode, index);
      var node = Object.prototype.hasOwnProperty.call(byKey, key) ? byKey[key] : null;
      if (node && compatible(node, vnode)) {
        delete byKey[key];
        patchNode(node, vnode, context, inSvg);
      } else {
        node = createNode(document, vnode, inSvg);
        node.__okey = key;
      }
      kept.push(node);
    });
    // Everything not kept goes: a key that disappeared, or a node the reconciler did not make.
    var token = {};
    kept.forEach(function (node) { node.__okept = token; });
    existing.forEach(function (node) {
      if (node.__okept !== token && node.parentNode === parent) { parent.removeChild(node); }
    });
    kept.forEach(function (node, index) {
      var at = parent.childNodes[index];
      if (at !== node) { parent.insertBefore(node, at || null); }
    });
  }

  var view = {
    h: h,
    /* Makes the children of container match a list of view nodes. options.refresh says the render
     * brings data the reader did not ask for, which protected subtrees wait out. */
    render: function (container, vnodes, options) {
      var context = { refresh: !!(options && options.refresh) };
      patchChildren(container, flatten(vnodes), context, false);
    },
    /* Whether a refresh left a protected subtree waiting since the last time this was asked. */
    takeDeferred: function () {
      var was = deferred;
      deferred = false;
      return was;
    }
  };

  opnview.view = view;
  opnview.h = h;
})(this);

/*
 * opnview front -- the detail: what opens when a name, a figure or a mark is picked (F7-26).
 *
 * INSPECTING IS NOT EDITING. A detail is anchored to the mark it belongs to -- a bottom sheet on a
 * phone -- and opening or closing it never changes the document. It carries the subject's facts and
 * its pivots: the related subjects (detail to detail), "scope this canvas to it" for an interface, a
 * client or a person the format can name, and "add a widget about it". The two pivots that edit are
 * explicit actions the canvas makes undoable; nothing here writes.
 *
 * A DETAIL OUTLIVES A REFRESH. It names its subject by the widget that drew it and the subject's id;
 * each drawing of that widget brings the subject's fresh facts, so the detail is redrawn with new
 * data and stays open. A subject the new answer no longer holds keeps its last facts and says so.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var h = opnview.h;
  var t = opnview.strings.t;

  /* Why a subject of a kind a canvas can be scoped to offers no scope: it has no key the format can
   * name -- a client known only by its address, an interface with no identifier, a person with no
   * name. Said in a word or two, where the pivot would be. */
  var NOT_SCOPABLE = {
    client: function () { return t("detail.not_scopable.client"); },
    interface: function () { return t("detail.not_scopable.interface"); },
    owner: function () { return t("detail.not_scopable.owner"); }
  };

  /* The detail of one subject. options: {subject, stale, canScope, canAdd, canFocus, onClose,
   * onOpen(subject), onScope(subject), onAdd(subject), onFocus(subject)}. */
  function render(options) {
    var subject = options.subject;
    var facts = (subject.facts || []).filter(Boolean);
    var related = (subject.related || []).filter(Boolean);
    var pivots = [];
    related.forEach(function (other) {
      pivots.push(h("button", { key: "open-" + other.kind + ":" + other.key, type: "button", "class": "btn btn-small pivot",
        "data-pivot": "open", on: { click: function () { options.onOpen(other); } } },
        t("detail.pivot.open", { name: other.title })));
    });
    if (options.canScope) {
      pivots.push(h("button", { key: "scope", type: "button", "class": "btn btn-small pivot", "data-pivot": "scope",
        on: { click: function () { options.onScope(subject); } } }, t("detail.pivot.scope")));
    } else if (!subject.reference && Object.prototype.hasOwnProperty.call(NOT_SCOPABLE, subject.kind)) {
      pivots.push(h("span", { key: "not-scopable", "class": "pivot-note", "data-pivot": "not_scopable" },
        NOT_SCOPABLE[subject.kind]()));
    }
    if (options.canFocus) {
      pivots.push(h("button", { key: "focus", type: "button", "class": "btn btn-small pivot", "data-pivot": "focus",
        on: { click: function () { options.onFocus(subject); } } }, t("detail.pivot.focus")));
    }
    if (options.canAdd) {
      pivots.push(h("button", { key: "add", type: "button", "class": "btn btn-small pivot", "data-pivot": "add",
        on: { click: function () { options.onAdd(subject); } } }, t("detail.pivot.add")));
    }
    return h("div", { key: "detail-pop", "class": "detail-pop", id: "detail-pop", role: "dialog",
      "aria-labelledby": "detail-title", "data-detail-subject": subject.kind + ":" + subject.key }, [
      h("div", { "class": "detail-head" }, [
        h("h2", { "class": "detail-title", id: "detail-title" }, subject.title),
        options.stale ? h("span", { "class": "badge badge-gap" }, t("detail.no_longer_answered")) : null,
        h("button", { type: "button", "class": "btn btn-small", "data-detail-close": "true",
          on: { click: options.onClose } }, t("action.close"))
      ]),
      h("div", { "class": "detail-body", tabindex: "-1" }, [
        facts.length ? h("dl", { key: "facts", "class": "popup-facts" }, facts.map(function (fact, index) {
          return h("div", { key: "f" + index, "class": "popup-fact" }, [h("dt", {}, fact[0]), h("dd", {}, fact[1])]);
        })) : null,
        subject.body || null,
        pivots.length ? h("div", { key: "pivots", "class": "detail-pivots" }, pivots) : null
      ])
    ]);
  }

  opnview.detail = { render: render };
})(this);

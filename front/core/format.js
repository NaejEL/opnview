/*
 * opnview front -- the formatters: a number, a volume in bytes, a rate in bits per second, an
 * instant, a duration, by the active language.
 *
 * A FIGURE IS DATA, ITS UNIT IS THE LANGUAGE'S. The browser's Intl formats both -- the digits, the
 * separators and the unit's name -- so no unit is written here. Where Intl is absent, as in the
 * interpreter the Go tests run the front in, the number is written plainly and the unit is the
 * catalogue's word for it (unit.symbol.*, unit.<duration unit>.*), so no word is a literal either
 * way.
 *
 * NOTHING IS ROUNDED THAT A READER WOULD COMPARE: a duration is spelled exactly, in every unit it
 * holds (410 seconds is 6 minutes 50 seconds), as the settings were always shown.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});

  var hasIntl = typeof Intl !== "undefined" && typeof Intl.NumberFormat === "function";
  /* Building an Intl formatter costs far more than using one: each is built once per language and
   * options, and kept. */
  var formatters = {};
  function cached(kind, options) {
    var key = kind + "|" + language() + "|" + JSON.stringify(options);
    if (!formatters[key]) {
      formatters[key] = kind === "date" ? new Intl.DateTimeFormat(language(), options) : new Intl.NumberFormat(language(), options);
    }
    return formatters[key];
  }
  var recorded = null;

  function note(text) {
    if (recorded) { recorded[text] = true; }
    return text;
  }

  function language() {
    return opnview.strings ? opnview.strings.language() : "en";
  }

  function plainNumber(value, fractionDigits) {
    var fixed = fractionDigits > 0 ? Number(value).toFixed(fractionDigits) : String(Math.round(value));
    if (fractionDigits > 0) { fixed = fixed.replace(/\.?0+$/, ""); }
    return fixed;
  }

  var BYTE_UNITS = ["byte", "kilobyte", "megabyte", "gigabyte", "terabyte", "petabyte"];
  var BIT_RATE_UNITS = ["bit-per-second", "kilobit-per-second", "megabit-per-second", "gigabit-per-second",
    "terabit-per-second"];

  function scaled(value, units) {
    var index = 0, magnitude = Math.abs(value);
    while (magnitude >= 1000 && index < units.length - 1) {
      magnitude /= 1000;
      value /= 1000;
      index++;
    }
    return { value: value, unit: units[index] };
  }

  function unitFormat(value, unit, fractionDigits) {
    if (hasIntl) {
      try {
        return cached("number", { style: "unit", unit: unit, unitDisplay: "short",
          maximumFractionDigits: fractionDigits }).format(value);
      } catch (e) { /* an engine without unit formatting falls through */ }
    }
    return plainNumber(value, fractionDigits) + " " + opnview.strings.tk("unit.symbol.", unit);
  }

  var DURATION_UNITS = [["day", 86400], ["hour", 3600], ["minute", 60], ["second", 1]];

  /* One count of one unit of a duration: the figure, then the catalogue's word for the unit in the
   * plural form the count takes. */
  function durationPart(unit, count) {
    var word;
    switch (unit) {
      case "day": word = opnview.strings.tp("unit.day", count); break;
      case "hour": word = opnview.strings.tp("unit.hour", count); break;
      case "minute": word = opnview.strings.tp("unit.minute", count); break;
      default: word = opnview.strings.tp("unit.second", count);
    }
    return format.number(count) + " " + word;
  }

  var format = {
    /* A count or a figure with no unit. */
    number: function (value, fractionDigits) {
      if (value === null || value === undefined) { return ""; }
      var digits = fractionDigits === undefined ? 0 : fractionDigits;
      if (hasIntl) {
        return note(cached("number", { maximumFractionDigits: digits }).format(value));
      }
      return note(plainNumber(value, digits));
    },
    /* A volume in bytes, in the decimal unit that holds it. */
    bytes: function (value) {
      var at = scaled(value, BYTE_UNITS);
      return note(unitFormat(at.value, at.unit, at.unit === "byte" ? 0 : 1));
    },
    /* A rate in bits per second, in the decimal unit that holds it. */
    rate: function (bitsPerSecond) {
      var at = scaled(bitsPerSecond, BIT_RATE_UNITS);
      return note(unitFormat(at.value, at.unit, at.unit === "bit-per-second" ? 0 : 1));
    },
    /* A share, 0 to 1, as a percentage. */
    percent: function (share, fractionDigits) {
      var digits = fractionDigits === undefined ? 1 : fractionDigits;
      if (hasIntl) {
        return note(cached("number", { style: "percent", maximumFractionDigits: digits }).format(share));
      }
      return note(plainNumber(share * 100, digits) + " " + opnview.strings.t("unit.symbol.percent"));
    },
    /* An instant given in UTC epoch seconds, in the reader's own time zone. */
    instant: function (epochSeconds) {
      if (epochSeconds === null || epochSeconds === undefined) { return ""; }
      var date = new Date(epochSeconds * 1000);
      if (hasIntl && typeof Intl.DateTimeFormat === "function") {
        return note(cached("date", { dateStyle: "medium", timeStyle: "medium" }).format(date));
      }
      return note(date.toISOString());
    },
    /* An instant on a chart's time axis, in the reader's own time zone: the time of day for a window
     * of a day or less, the day and the time for a week, the day for anything longer. */
    tick: function (epochSeconds, spanSeconds) {
      if (epochSeconds === null || epochSeconds === undefined) { return ""; }
      var date = new Date(epochSeconds * 1000);
      if (hasIntl && typeof Intl.DateTimeFormat === "function") {
        var options = spanSeconds <= 86400 ? { hour: "2-digit", minute: "2-digit" }
          : spanSeconds <= 7 * 86400 ? { weekday: "short", hour: "2-digit" } : { month: "short", day: "numeric" };
        return note(cached("date", options).format(date));
      }
      var iso = date.toISOString();
      return note(spanSeconds <= 86400 ? iso.slice(11, 16) : spanSeconds <= 7 * 86400 ? iso.slice(5, 13) : iso.slice(5, 10));
    },
    /* A time of day of the reader's clock, for the live indicator. */
    clock: function (date) {
      if (hasIntl && typeof Intl.DateTimeFormat === "function") {
        return note(cached("date", { timeStyle: "medium" }).format(date));
      }
      return note(date.toISOString().slice(11, 19));
    },
    /* A number of seconds spelled exactly in every unit it holds, longest first. */
    duration: function (seconds) {
      if (seconds === null || seconds === undefined) { return ""; }
      var rest = Math.max(0, Math.round(seconds)), parts = [];
      DURATION_UNITS.forEach(function (unit) {
        var count = Math.floor(rest / unit[1]);
        if (count > 0) {
          parts.push(durationPart(unit[0], count));
          rest -= count * unit[1];
        }
      });
      if (parts.length === 0) { parts.push(durationPart("second", 0)); }
      return note(parts.join(" "));
    },
    /* Records every text the formatters produce, for a test that tells a formatted figure from a
     * literal; called with false to stop. */
    record: function (on) {
      recorded = on ? {} : null;
    },
    recorded: function () { return recorded ? Object.keys(recorded) : []; },
    /* The units whose catalogue word the fallback writes (unit.symbol.<unit>), for a test. */
    symbolUnits: function () { return BYTE_UNITS.concat(BIT_RATE_UNITS, ["percent"]); }
  };

  opnview.format = format;
})(this);

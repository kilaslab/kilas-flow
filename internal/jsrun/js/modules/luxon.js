// KilasFlow's own code, written for internal/jsrun from n8n's documented
// Code-node behaviour and Luxon's documented API. It is not derived from
// n8n's source.
//
// n8n offers Luxon to code as the globals DateTime, Duration, Interval, Info
// and Settings, with $now and $today as DateTimes, all in the workflow's time
// zone. The vendored bundle (third_party/luxon) is loaded:
//
//   - before the code runs, and free, when the analysis saw the code name
//     Luxon; this module then loads it during setup;
//   - otherwise on first use of one of the globals, charged to the code, as
//     any work the code does is.
//
// Either way it is configured the moment it loads, before anything reads it:
// Settings.defaultZone is the workflow's zone (UTC when it has none) and
// Settings.defaultLocale is en-US. require('luxon') answers with the same
// configured instance.
(function (kit) {
  'use strict';

  var defineProperty = Object.defineProperty;
  var global = kit.global;
  var globals = ['DateTime', 'Duration', 'Interval', 'Info', 'Settings'];
  var exported = ['DateTime', 'Duration', 'FixedOffsetZone', 'IANAZone', 'Info', 'Interval', 'InvalidZone', 'Settings', 'SystemZone', 'VERSION', 'Zone'];

  // The zone Luxon defaults to: the workflow's, when it names a zone, else
  // UTC, as for $now in an expression.
  function workflowZone() {
    var zone = kit.snapshot.timezone;
    if (!zone) return 'UTC';
    try {
      kit.native('intl.zone', String(zone));
      return String(zone);
    } catch (_) {
      return 'UTC';
    }
  }

  var luxon;
  function load() {
    if (luxon === undefined) {
      var loaded = kit.library('luxon');
      loaded.Settings.defaultZone = workflowZone();
      loaded.Settings.defaultLocale = 'en-US';
      luxon = loaded;
    }
    return luxon;
  }

  // A global the code may read, or replace with its own value.
  function data(name, value) {
    defineProperty(global, name, { value: value, writable: true, configurable: true, enumerable: false });
  }

  // settle turns the lazy globals still in place into plain values.
  var lazy = {};
  function settle() {
    var library = load();
    globals.forEach(function (name) {
      var current = Object.getOwnPropertyDescriptor(global, name);
      if (current && current.get === lazy[name]) data(name, library[name]);
    });
  }

  var preloaded = kit.snapshot.preload.indexOf('luxon') >= 0;
  if (preloaded) {
    var library = load();
    globals.forEach(function (name) { data(name, library[name]); });
  } else {
    globals.forEach(function (name) {
      lazy[name] = function () {
        settle();
        return global[name];
      };
      defineProperty(global, name, {
        get: lazy[name],
        set: function (value) { data(name, value); },
        configurable: true, enumerable: false,
      });
    });
  }

  // $now and $today are read fresh each time, as in n8n.
  [['$now', function () { return load().DateTime.now(); }],
    ['$today', function () { return load().DateTime.now().startOf('day'); }]].forEach(function (entry) {
    defineProperty(global, entry[0], {
      get: entry[1],
      set: function (value) { data(entry[0], value); },
      configurable: true, enumerable: false,
    });
  });

  // What require('luxon') answers: the instance itself when it is already
  // loaded, else an object whose members load it on first use.
  if (preloaded) return luxon;
  var facade = {};
  exported.forEach(function (name) {
    defineProperty(facade, name, {
      get: function () {
        var library = load();
        exported.forEach(function (key) {
          defineProperty(facade, key, { value: library[key], writable: true, configurable: true, enumerable: true });
        });
        return library[name];
      },
      configurable: true, enumerable: true,
    });
  });
  return facade;
})

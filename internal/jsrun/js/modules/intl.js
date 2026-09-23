// KilasFlow's own code, written for internal/jsrun from ECMA-402 and Node's
// documented behaviour. It is not derived from any other implementation.
//
// goja has no Intl, and its own locale methods are silently wrong, so this
// module provides the part of Intl that Code nodes and Luxon use, over Go
// natives (intl.go):
//
//   - Intl.DateTimeFormat, in en-US only. Any other locale is a RangeError,
//     never English text answering a German request.
//   - Intl.NumberFormat, in any locale golang.org/x/text knows.
//   - Intl.Locale, for the week information Luxon's locale weeks read.
//   - Intl.RelativeTimeFormat refuses every locale by name. Luxon formats
//     English relative times itself, as it does in Node; without this, it
//     would answer a German request in English too.
//
// Number, Date and String's locale methods are routed through the same
// code. An option the natives cannot honour is an error, not a guess.
(function (kit) {
  'use strict';

  if (typeof kit.global.Intl !== 'undefined') return undefined;

  var native = kit.native;
  var DateType = Date;
  var defineProperty = Object.defineProperty;
  var ObjectType = Object;
  var StringType = String;
  var NumberType = Number;
  var WeakMapType = WeakMap;
  var weakGet = WeakMap.prototype.get;
  var weakSet = WeakMap.prototype.set;
  var dateNow = Date.now;
  var dateGetTime = Date.prototype.getTime;
  var numberValueOf = Number.prototype.valueOf;
  var BigIntType = typeof BigInt === 'function' ? BigInt : undefined;
  var bigIntValueOf = BigIntType ? BigIntType.prototype.valueOf : undefined;
  var apply = kit.apply;
  var floor = Math.floor;
  var abs = Math.abs;
  var getOwnPropertyDescriptor = Object.getOwnPropertyDescriptor;
  var emptyOptions = {};

  // Every VM runs this module, and most code never formats a date or a
  // number, so everything below is made on first use: the Intl global is an
  // accessor until it is read, and the built-ins' locale methods call into
  // what build() returns.
  var built;
  function implementation() {
    if (built === undefined) built = build();
    return built;
  }

  function build() {
    // ---- Shared -----------------------------------------------------------------

    // The zone Dates format in by default: the workflow's, else UTC. An
    // unknown zone is UTC, as it is for $now in an expression, because the
    // workflow's zone was checked when it was saved.
    var defaultZone;
    function workflowZone() {
      if (defaultZone === undefined) {
        defaultZone = 'UTC';
        if (kit.snapshot.timezone) {
          try { defaultZone = native('intl.zone', StringType(kit.snapshot.timezone)); } catch (_) { defaultZone = 'UTC'; }
        }
      }
      return defaultZone;
    }

    // localeList reads the locales argument into a list of strings, the part of
    // CanonicalizeLocaleList that needs JavaScript; the natives canonicalise.
    function localeList(locales) {
      if (locales === undefined) return [];
      if (typeof locales === 'string') return [locales];
      if (locales instanceof Locale) return [locales.toString()];
      if (locales === null) throw new TypeError('Cannot convert undefined or null to object');
      var list = ObjectType(locales);
      var length = kit.lengthOf(list);
      if (length > 100) throw kit.tooLarge('a list of locales', length, 100);
      var out = [];
      for (var index = 0; index < length; index++) {
        if (!(index in list)) continue;
        var item = list[index];
        if (item instanceof Locale) {
          out.push(item.toString());
        } else if (typeof item === 'string' || (typeof item === 'object' && item !== null)) {
          out.push(StringType(item));
        } else {
          throw new TypeError('Language ID should be string or object.');
        }
      }
      return out;
    }

    function optionsObject(options) {
      if (options === undefined) return ObjectType.create(null);
      if (options === null) throw new TypeError('Cannot convert undefined or null to object');
      return ObjectType(options);
    }

    function getOption(owner, options, name, type, values, fallback) {
      var value = options[name];
      if (value === undefined) return fallback;
      value = type === 'boolean' ? !!value : StringType(value);
      if (values !== undefined && values.indexOf(value) < 0) {
        throw new RangeError('Value ' + value + ' out of range for ' + owner + ' options property ' + name);
      }
      return value;
    }

    function getNumberOption(options, name, minimum, maximum, fallback) {
      var value = options[name];
      if (value === undefined) return fallback;
      value = NumberType(value);
      if (value !== value || value < minimum || value > maximum) {
        throw new RangeError(name + ' value is out of range.');
      }
      return floor(value);
    }

    // parts turns a native's flat [type, value, …] list into part objects.
    function parts(flat) {
      var out = [];
      for (var index = 0; index < flat.length; index += 2) out.push({ type: flat[index], value: flat[index + 1] });
      return out;
    }

    function defineMethods(target, methods) {
      ObjectType.keys(methods).forEach(function (name) {
        defineProperty(target, name, { value: methods[name], writable: true, configurable: true, enumerable: false });
      });
    }

    function toStringTag(target, tag) {
      defineProperty(target, Symbol.toStringTag, { value: tag, configurable: true });
    }

    // Each object's state lives here, out of the code's reach.
    var states = new WeakMapType();
    function stateOf(object, type, method) {
      var state = (object !== null && typeof object === 'object') ? apply(weakGet, states, [object]) : undefined;
      if (state === undefined || state.type !== type) {
        throw new TypeError('Method ' + method + ' called on incompatible receiver ' + kit.inspect(object, 1));
      }
      return state;
    }

    // ---- Intl.DateTimeFormat --------------------------------------------------------

    var DTF = 'Intl.DateTimeFormat';
    var componentOptions = [
      ['weekday', ['narrow', 'short', 'long']],
      ['era', ['narrow', 'short', 'long']],
      ['year', ['2-digit', 'numeric']],
      ['month', ['2-digit', 'numeric', 'narrow', 'short', 'long']],
      ['day', ['2-digit', 'numeric']],
      ['dayPeriod', ['narrow', 'short', 'long']],
      ['hour', ['2-digit', 'numeric']],
      ['minute', ['2-digit', 'numeric']],
      ['second', ['2-digit', 'numeric']],
      ['fractionalSecondDigits'],
      ['timeZoneName', ['short', 'long', 'shortOffset', 'longOffset', 'shortGeneric', 'longGeneric']],
    ];
    var styles = ['full', 'long', 'medium', 'short'];
    var typeSequence = /^[a-zA-Z0-9]{3,8}(-[a-zA-Z0-9]{3,8})*$/;

    // createDateTimeFormat is CreateDateTimeFormat: it reads the options in the
    // order ECMA-402 reads them, and the native resolves them.
    function createDateTimeFormat(object, locales, options, required, defaults) {
      var list = localeList(locales);
      options = optionsObject(options);
      var record = { required: required, defaults: defaults, defaultZone: workflowZone() };
      getOption(DTF, options, 'localeMatcher', 'string', ['lookup', 'best fit'], 'best fit');
      ['calendar', 'numberingSystem'].forEach(function (name) {
        var value = getOption(DTF, options, name, 'string', undefined, undefined);
        if (value !== undefined && !typeSequence.test(value)) throw new RangeError('Invalid ' + name + ' : ' + value);
        if (value !== undefined) record[name] = value.toLowerCase();
      });
      var hour12 = getOption(DTF, options, 'hour12', 'boolean', undefined, undefined);
      var hourCycle = getOption(DTF, options, 'hourCycle', 'string', ['h11', 'h12', 'h23', 'h24'], undefined);
      if (hour12 !== undefined) record.hour12 = hour12;
      else if (hourCycle !== undefined) record.hourCycle = hourCycle;
      var timeZone = options.timeZone;
      if (timeZone !== undefined) record.timeZone = StringType(timeZone);
      componentOptions.forEach(function (entry) {
        var value = entry[0] === 'fractionalSecondDigits'
          ? getNumberOption(options, entry[0], 1, 3, undefined)
          : getOption(DTF, options, entry[0], 'string', entry[1], undefined);
        if (value !== undefined) record[entry[0]] = value;
      });
      getOption(DTF, options, 'formatMatcher', 'string', ['basic', 'best fit'], 'best fit');
      var dateStyle = getOption(DTF, options, 'dateStyle', 'string', styles, undefined);
      var timeStyle = getOption(DTF, options, 'timeStyle', 'string', styles, undefined);
      if (dateStyle !== undefined) record.dateStyle = dateStyle;
      if (timeStyle !== undefined) record.timeStyle = timeStyle;
      var resolved = native('intl.dateTimeFormat', list, record);
      var state = { type: DTF, pattern: resolved.pattern, zone: resolved.zone, resolved: resolved, bound: undefined };
      apply(weakSet, states, [object, state]);
      return object;
    }

    var resolvedOrder = ['locale', 'calendar', 'numberingSystem', 'timeZone', 'hourCycle', 'hour12', 'weekday', 'era', 'year', 'month', 'day',
      'dayPeriod', 'hour', 'minute', 'second', 'fractionalSecondDigits', 'timeZoneName', 'dateStyle', 'timeStyle'];

    // timeOf is ToNumber and TimeClip of format's argument.
    function timeOf(date) {
      var time = date === undefined ? apply(dateNow, DateType, []) : NumberType(date);
      if (time !== time || abs(time) > 8.64e15) throw new RangeError('Invalid time value');
      time = time < 0 ? -floor(-time) : floor(time);
      return time === 0 ? 0 : time;
    }

    function formatDate(state, date, asParts) {
      return native('intl.formatDate', state.pattern, state.zone, timeOf(date), asParts);
    }

    function DateTimeFormat(locales, options) {
      var object = this instanceof DateTimeFormat ? this : ObjectType.create(DateTimeFormat.prototype);
      return createDateTimeFormat(object, locales, options, 'any', 'date');
    }
    defineProperty(DateTimeFormat, 'length', { value: 0 });
    defineMethods(DateTimeFormat.prototype, {
      formatToParts: function formatToParts(date) {
        return parts(formatDate(stateOf(this, DTF, 'Intl.DateTimeFormat.prototype.formatToParts'), date, true));
      },
      resolvedOptions: function resolvedOptions() {
        var resolved = stateOf(this, DTF, 'Intl.DateTimeFormat.prototype.resolvedOptions').resolved;
        var out = {};
        resolvedOrder.forEach(function (key) { if (resolved[key] !== undefined) out[key] = resolved[key]; });
        return out;
      },
      // Ranges ("Mar 1 – 5, 2026") need CLDR's interval formats, which are
      // not shipped; they are refused by name, as Luxon's Interval reaches them.
      formatRange: function formatRange() {
        stateOf(this, DTF, 'Intl.DateTimeFormat.prototype.formatRange');
        throw new RangeError('Intl.DateTimeFormat.prototype.formatRange is not supported');
      },
      formatRangeToParts: function formatRangeToParts() {
        stateOf(this, DTF, 'Intl.DateTimeFormat.prototype.formatRangeToParts');
        throw new RangeError('Intl.DateTimeFormat.prototype.formatRangeToParts is not supported');
      },
    });
    defineProperty(DateTimeFormat.prototype.formatRange, 'length', { value: 2 });
    defineProperty(DateTimeFormat.prototype.formatRangeToParts, 'length', { value: 2 });
    defineProperty(DateTimeFormat.prototype, 'format', {
      get: function () {
        var state = stateOf(this, DTF, 'get Intl.DateTimeFormat.prototype.format');
        if (state.bound === undefined) {
          state.bound = function (date) { return formatDate(state, date, false); };
          defineProperty(state.bound, 'name', { value: '' });
        }
        return state.bound;
      },
      configurable: true, enumerable: false,
    });
    toStringTag(DateTimeFormat.prototype, 'Intl.DateTimeFormat');

    // ---- Intl.NumberFormat ---------------------------------------------------------------

    var NF = 'Intl.NumberFormat';

    function createNumberFormat(object, locales, options) {
      var list = localeList(locales);
      options = optionsObject(options);
      var record = {};
      getOption(NF, options, 'localeMatcher', 'string', ['lookup', 'best fit'], 'best fit');
      var numbering = getOption(NF, options, 'numberingSystem', 'string', undefined, undefined);
      if (numbering !== undefined) {
        if (!typeSequence.test(numbering)) throw new RangeError('Invalid numberingSystem : ' + numbering);
        record.numberingSystem = numbering;
      }
      record.style = getOption(NF, options, 'style', 'string', ['decimal', 'percent', 'currency', 'unit'], 'decimal');
      var currency = getOption(NF, options, 'currency', 'string', undefined, undefined);
      if (currency !== undefined) record.currency = currency;
      record.currencyDisplay = getOption(NF, options, 'currencyDisplay', 'string', ['code', 'symbol', 'narrowSymbol', 'name'], 'symbol');
      record.currencySign = getOption(NF, options, 'currencySign', 'string', ['standard', 'accounting'], 'standard');
      getOption(NF, options, 'unit', 'string', undefined, undefined);
      getOption(NF, options, 'unitDisplay', 'string', ['short', 'narrow', 'long'], 'short');
      record.notation = getOption(NF, options, 'notation', 'string', ['standard', 'scientific', 'engineering', 'compact'], 'standard');
      record.minimumIntegerDigits = getNumberOption(options, 'minimumIntegerDigits', 1, 21, 1);
      ['minimumFractionDigits', 'maximumFractionDigits'].forEach(function (name) {
        var value = getNumberOption(options, name, 0, 100, undefined);
        if (value !== undefined) record[name] = value;
      });
      ['minimumSignificantDigits', 'maximumSignificantDigits'].forEach(function (name) {
        var value = getNumberOption(options, name, 1, 21, undefined);
        if (value !== undefined) record[name] = value;
      });
      var increment = getNumberOption(options, 'roundingIncrement', 1, 5000, 1);
      if ([1, 2, 5, 10, 20, 25, 50, 100, 200, 250, 500, 1000, 2000, 2500, 5000].indexOf(increment) < 0) {
        throw new RangeError('roundingIncrement value is out of range.');
      }
      record.roundingIncrement = increment;
      record.roundingMode = getOption(NF, options, 'roundingMode', 'string',
        ['ceil', 'floor', 'expand', 'trunc', 'halfCeil', 'halfFloor', 'halfExpand', 'halfTrunc', 'halfEven'], 'halfExpand');
      record.roundingPriority = getOption(NF, options, 'roundingPriority', 'string', ['auto', 'morePrecision', 'lessPrecision'], 'auto');
      record.trailingZeroDisplay = getOption(NF, options, 'trailingZeroDisplay', 'string', ['auto', 'stripIfInteger'], 'auto');
      getOption(NF, options, 'compactDisplay', 'string', ['short', 'long'], 'short');
      var grouping = options.useGrouping;
      if (grouping === undefined) {
        record.useGrouping = 'auto';
      } else if (grouping === true) {
        record.useGrouping = 'always';
      } else if (!grouping) {
        record.useGrouping = false;
      } else {
        grouping = StringType(grouping);
        if (grouping === 'true' || grouping === 'false') record.useGrouping = 'auto';
        else if (['min2', 'auto', 'always'].indexOf(grouping) >= 0) record.useGrouping = grouping;
        else throw new RangeError('Value ' + grouping + ' out of range for Intl.NumberFormat options property useGrouping');
      }
      record.signDisplay = getOption(NF, options, 'signDisplay', 'string', ['auto', 'never', 'always', 'exceptZero', 'negative'], 'auto');
      var resolved = native('intl.numberFormat', list, record);
      apply(weakSet, states, [object, { type: NF, locales: list, record: record, resolved: resolved, bound: undefined }]);
      return object;
    }

    var numberOrder = ['locale', 'numberingSystem', 'style', 'currency', 'currencyDisplay', 'currencySign', 'unit', 'unitDisplay',
      'minimumIntegerDigits', 'minimumFractionDigits', 'maximumFractionDigits', 'minimumSignificantDigits', 'maximumSignificantDigits',
      'useGrouping', 'notation', 'compactDisplay', 'signDisplay', 'roundingIncrement', 'roundingMode', 'roundingPriority', 'trailingZeroDisplay'];

    // mathematicalValue is ToIntlMathematicalValue: BigInts and strings are
    // formatted exactly, anything else as a Number.
    function mathematicalValue(value) {
      if (typeof value === 'bigint') return StringType(value);
      if (typeof value === 'string') return value;
      if (typeof value === 'object' && value !== null) {
        var primitive = value.valueOf();
        if (typeof primitive === 'bigint' || typeof primitive === 'string') return mathematicalValue(primitive);
      }
      return NumberType(value);
    }

    function formatNumber(state, value, asParts) {
      return native('intl.formatNumber', state.locales, state.record, mathematicalValue(value), asParts);
    }

    function NumberFormat(locales, options) {
      var object = this instanceof NumberFormat ? this : ObjectType.create(NumberFormat.prototype);
      return createNumberFormat(object, locales, options);
    }
    defineProperty(NumberFormat, 'length', { value: 0 });
    defineMethods(NumberFormat.prototype, {
      formatToParts: function formatToParts(value) {
        return parts(formatNumber(stateOf(this, NF, 'Intl.NumberFormat.prototype.formatToParts'), value, true));
      },
      resolvedOptions: function resolvedOptions() {
        var resolved = stateOf(this, NF, 'Intl.NumberFormat.prototype.resolvedOptions').resolved;
        var out = {};
        numberOrder.forEach(function (key) { if (resolved[key] !== undefined) out[key] = resolved[key]; });
        return out;
      },
    });
    defineProperty(NumberFormat.prototype, 'format', {
      get: function () {
        var state = stateOf(this, NF, 'get Intl.NumberFormat.prototype.format');
        if (state.bound === undefined) {
          state.bound = function (value) { return formatNumber(state, value, false); };
          defineProperty(state.bound, 'name', { value: '' });
        }
        return state.bound;
      },
      configurable: true, enumerable: false,
    });
    toStringTag(NumberFormat.prototype, 'Intl.NumberFormat');

    // ---- Intl.Locale ---------------------------------------------------------------------

    // Locale carries a canonical tag and the week information Luxon's locale
    // weeks read. Options that would edit the tag are refused.
    function Locale(tag, options) {
      if (!(this instanceof Locale)) throw new TypeError("Constructor Intl.Locale requires 'new'");
      if (typeof tag !== 'string' && !(typeof tag === 'object' && tag !== null)) {
        throw new TypeError('First argument to Intl.Locale constructor can\'t be empty or missing');
      }
      if (options !== undefined && ObjectType.keys(ObjectType(options)).length > 0) {
        throw new RangeError('Intl.Locale options are not supported');
      }
      var canonical = native('intl.locales', [StringType(tag)])[0];
      apply(weakSet, states, [this, { type: 'Intl.Locale', tag: canonical }]);
    }
    function localeTag(object, method) { return stateOf(object, 'Intl.Locale', method).tag; }
    function subtags(tag) {
      var main = tag.split('-u-')[0].split('-x-')[0].split('-');
      var out = { language: main[0], script: undefined, region: undefined };
      for (var index = 1; index < main.length; index++) {
        if (/^[A-Za-z]{4}$/.test(main[index])) out.script = main[index];
        else if (/^([A-Za-z]{2}|[0-9]{3})$/.test(main[index])) out.region = main[index];
      }
      return out;
    }
    defineMethods(Locale.prototype, {
      toString: function toString() { return localeTag(this, 'Intl.Locale.prototype.toString'); },
      getWeekInfo: function getWeekInfo() { return native('intl.weekInfo', localeTag(this, 'Intl.Locale.prototype.getWeekInfo')); },
    });
    [['baseName', function (tag) { return tag.split('-u-')[0].split('-x-')[0]; }],
      ['language', function (tag) { return subtags(tag).language; }],
      ['script', function (tag) { return subtags(tag).script; }],
      ['region', function (tag) { return subtags(tag).region; }],
      ['weekInfo', function (tag) { return native('intl.weekInfo', tag); }]].forEach(function (entry) {
      defineProperty(Locale.prototype, entry[0], {
        get: function () { return entry[1](localeTag(this, 'get Intl.Locale.prototype.' + entry[0])); },
        configurable: true, enumerable: false,
      });
    });
    toStringTag(Locale.prototype, 'Intl.Locale');

    // ---- Intl.RelativeTimeFormat -----------------------------------------------------------

    // Present only to refuse. Luxon formats English relative times itself and
    // reaches for this only for another language, where answering in English
    // would be silently wrong.
    function RelativeTimeFormat(locales) {
      var list = native('intl.locales', localeList(locales));
      throw new RangeError('relative time formatting in locale ' + (list[0] || 'en-US') + ' is not supported');
    }

    // ---- Intl --------------------------------------------------------------------------------

    function supportedLocalesOf(check) {
      return function supportedLocalesOf(locales) {
        return native('intl.locales', localeList(locales)).filter(check);
      };
    }
    var english = /^en(-US)?(-u-|$)/;
    defineMethods(DateTimeFormat, { supportedLocalesOf: supportedLocalesOf(function (tag) { return english.test(tag); }) });
    defineMethods(NumberFormat, { supportedLocalesOf: supportedLocalesOf(function () { return true; }) });

    var Intl = {};
    defineMethods(Intl, {
      DateTimeFormat: DateTimeFormat,
      NumberFormat: NumberFormat,
      Locale: Locale,
      RelativeTimeFormat: RelativeTimeFormat,
      getCanonicalLocales: function getCanonicalLocales(locales) { return native('intl.locales', localeList(locales)); },
    });
    toStringTag(Intl, 'Intl');

    // ---- The built-ins' locale methods ---------------------------------------------------------

    // Formatters for the calls without locales or options, which are most of
    // them, are made once.
    var defaults = ObjectType.create(null);
    function cached(key, make) {
      if (defaults[key] === undefined) defaults[key] = make();
      return defaults[key];
    }

    function dateToLocale(name, required, which, date, locales, options) {
      var time = apply(dateGetTime, date, []);
      if (time !== time) return 'Invalid Date';
      var format;
      if (locales === undefined && options === undefined) {
        format = cached(name, function () { return createDateTimeFormat(ObjectType.create(DateTimeFormat.prototype), undefined, undefined, required, which); });
      } else {
        format = createDateTimeFormat(ObjectType.create(DateTimeFormat.prototype), locales, options, required, which);
      }
      return formatDate(apply(weakGet, states, [format]), time, false);
    }

    function numberToLocale(value, locales, options) {
      var format = locales === undefined && options === undefined
        ? cached('number', function () { return createNumberFormat(ObjectType.create(NumberFormat.prototype), undefined, undefined); })
        : createNumberFormat(ObjectType.create(NumberFormat.prototype), locales, options);
      return formatNumber(apply(weakGet, states, [format]), value, false);
    }

    var collatorOptions = [
      ['usage', ['sort', 'search']], ['localeMatcher', ['lookup', 'best fit']], ['collation'], ['numeric'],
      ['caseFirst', ['upper', 'lower', 'false']], ['sensitivity', ['base', 'accent', 'case', 'variant']], ['ignorePunctuation'],
    ];
    function compareStrings(left, right, locales, options) {
      var list = localeList(locales);
      options = optionsObject(options);
      var record = {};
      collatorOptions.forEach(function (entry) {
        var name = entry[0];
        var type = name === 'numeric' || name === 'ignorePunctuation' ? 'boolean' : 'string';
        var value = getOption('Intl.Collator', options, name, type, entry[1], undefined);
        if (value !== undefined) record[name] = value;
      });
      return native('intl.compare', left, right, list, record);
    }

    return { Intl: Intl, dateToLocale: dateToLocale, numberToLocale: numberToLocale, compareStrings: compareStrings };
  }

  // ---- Setup: the Intl global and the built-ins' locale methods ---------------------------

  defineProperty(kit.global, 'Intl', {
    get: function () {
      var value = implementation().Intl;
      var current = getOwnPropertyDescriptor(kit.global, 'Intl');
      if (current && current.get !== undefined) kit.define('Intl', value);
      return value;
    },
    set: function (value) { kit.define('Intl', value); },
    configurable: true, enumerable: false,
  });

  // replace swaps a built-in method, keeping its name and length.
  function replace(owner, name, length, method) {
    defineProperty(method, 'name', { value: name });
    defineProperty(method, 'length', { value: length });
    defineProperty(owner, name, { value: method, writable: true, configurable: true, enumerable: false });
  }

  [['toLocaleString', 'any', 'all'], ['toLocaleDateString', 'date', 'date'], ['toLocaleTimeString', 'time', 'time']].forEach(function (entry) {
    replace(DateType.prototype, entry[0], 0, function (locales, options) {
      return implementation().dateToLocale(entry[0], entry[1], entry[2], this, locales, options);
    });
  });
  replace(NumberType.prototype, 'toLocaleString', 0, function (locales, options) {
    return implementation().numberToLocale(apply(numberValueOf, this, []), locales, options);
  });
  if (BigIntType) {
    replace(BigIntType.prototype, 'toLocaleString', 0, function (locales, options) {
      return implementation().numberToLocale(apply(bigIntValueOf, this, []), locales, options);
    });
  }
  replace(StringType.prototype, 'localeCompare', 1, function (that, locales, options) {
    if (this === undefined || this === null) throw new TypeError('String.prototype.localeCompare called on null or undefined');
    var left = StringType(this);
    var right = StringType(that);
    // The common call needs nothing built: it goes straight to the native.
    if (locales === undefined && options === undefined) return native('intl.compare', left, right, [], emptyOptions);
    return implementation().compareStrings(left, right, locales, options);
  });

  return undefined;
})

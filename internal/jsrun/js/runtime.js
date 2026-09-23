// KilasFlow's own code, written for internal/jsrun from n8n's documented
// Code-node behaviour. It is not derived from n8n's source.
//
// Everything here is trusted: it runs before any user code, in the same VM,
// and the runner reaches it only through the object this script returns. A
// script cannot replace these functions. It can change the built-ins they
// use, which is charged to its own time limit and can only spoil its own
// result, so the built-ins that matter are captured up front anyway.
(function () {
  'use strict';

  var isArray = Array.isArray;
  var stringifyJSON = JSON.stringify;
  var parseJSON = JSON.parse;
  var apply = Reflect.apply;
  var defineProperty = Object.defineProperty;
  var ownKeys = Object.keys;
  var ErrorType = Error;
  var DateType = Date;
  var MapType = Map;
  var SetType = Set;
  var RegExpType = RegExp;
  var ProxyType = Proxy;
  var WeakMapType = WeakMap;
  var weakGet = WeakMap.prototype.get;
  var weakSet = WeakMap.prototype.set;
  var weakHas = WeakMap.prototype.has;
  var global = globalThis;

  // A thrown value the runner recognises by identity.
  var outputLimit = {};

  function InvalidReturn(message) {
    this.message = message;
  }

  function kindOf(value) {
    if (value === null) return 'null';
    if (isArray(value)) return 'a list';
    if (typeof value === 'object') return 'an object';
    if (typeof value === 'undefined') return 'nothing';
    return 'a ' + typeof value;
  }

  function isObject(value) {
    return value !== null && typeof value === 'object' && !isArray(value);
  }

  // ---- Lineage -------------------------------------------------------------

  // origins remembers which input item each input object is, so an item the
  // code returns keeps its own lineage however it was reordered or filtered.
  // The json object is remembered too, because `{ json: item.json }` is a
  // common way to pass an item on.
  var origins = new WeakMapType();

  function remember(object, index) {
    if (object !== null && typeof object === 'object' && !apply(weakHas, origins, [object])) {
      apply(weakSet, origins, [object, index]);
    }
  }

  function originOf(object) {
    if (object === null || typeof object !== 'object') return undefined;
    return apply(weakGet, origins, [object]);
  }

  // explicitPair reads a returned pairedItem: an item index, { item }, or a
  // list of those. Several sources cannot be represented as one lineage, so
  // they are recorded as lost rather than guessed.
  function explicitPair(pairedItem, count, where) {
    var pointer = pairedItem;
    if (isArray(pointer)) {
      if (pointer.length === 0) return undefined;
      if (pointer.length > 1) return 'lost';
      pointer = pointer[0];
    }
    var index = pointer;
    if (isObject(pointer)) {
      if (pointer.input !== undefined && pointer.input !== 0) {
        throw new InvalidReturn(where + ' has a pairedItem naming input ' + pointer.input + ', but a Code node has one input');
      }
      index = pointer.item;
    }
    if (typeof index !== 'number' || index % 1 !== 0 || index < 0 || index >= count) {
      throw new InvalidReturn(where + ' has a pairedItem pointing at item ' + index + ', but the node was given ' + count + ' items');
    }
    return index;
  }

  // ---- Return normalisation -------------------------------------------------

  // toItem accepts an item, or a plain object that becomes the item's json.
  function toItem(entry, where, eachItem, index, count) {
    if (!isObject(entry)) {
      throw new InvalidReturn(where + ' is ' + kindOf(entry) + ', but every returned item must be an object');
    }
    var item;
    if ('json' in entry) {
      if (!isObject(entry.json)) {
        throw new InvalidReturn(where + ' has a json that is ' + kindOf(entry.json) + ', but json must be an object');
      }
      item = { json: entry.json };
      if (entry.binary !== undefined && entry.binary !== null) {
        if (!isObject(entry.binary)) {
          throw new InvalidReturn(where + ' has a binary that is ' + kindOf(entry.binary) + ', but binary must be an object');
        }
        item.binary = entry.binary;
      }
    } else {
      item = { json: entry };
    }
    var paired;
    if (entry.pairedItem !== undefined && entry.pairedItem !== null && item.json !== entry) {
      paired = explicitPair(entry.pairedItem, count, where);
    } else if (eachItem) {
      paired = index;
    } else {
      paired = originOf(entry);
      if (paired === undefined) paired = originOf(item.json);
    }
    if (paired !== undefined) item.paired = paired;
    return item;
  }

  function normalise(result, eachItem, index, count) {
    if (eachItem) {
      if (result === undefined || result === null) {
        throw new InvalidReturn('the code returned ' + kindOf(result) + '; when it runs once for each item it must return one object');
      }
      if (isArray(result)) {
        throw new InvalidReturn('the code returned a list; when it runs once for each item it must return one object');
      }
      return [toItem(result, 'the returned value', true, index, count)];
    }
    if (isArray(result)) {
      var items = [];
      for (var position = 0; position < result.length; position++) {
        items.push(toItem(result[position], 'item ' + position, false, 0, count));
      }
      return items;
    }
    if (isObject(result)) {
      return [toItem(result, 'the returned value', false, 0, count)];
    }
    throw new InvalidReturn('the code returned ' + kindOf(result) + '; it must return a list of items, such as [{ json: { … } }]');
  }

  // stringify is JSON.stringify with a running size count, so an oversized
  // result stops early instead of being built whole before Go measures it.
  //
  // The count is a lower bound on the bytes written: exact for numbers,
  // booleans, null and punctuation, and never more than a string's UTF-8
  // length. Stopping on it therefore never refuses output that would have
  // fitted, and Go checks the exact size of whatever gets through.
  function stringify(value, max) {
    var size = 0;
    return stringifyJSON(value, function (key, current) {
      if (key !== '') {
        size += 1; // the comma or bracket before the value
        if (!isArray(this)) size += key.length + 3; // "key":
      }
      switch (typeof current) {
        case 'string': size += current.length + 2; break;
        case 'number': size += isFinite(current) ? String(current).length : 4; break;
        case 'boolean': size += current ? 4 : 5; break;
        case 'object': size += current === null ? 4 : 2; break;
      }
      if (size > max) throw outputLimit;
      return current;
    });
  }

  // describe turns anything thrown into plain strings the runner can read
  // without calling back into the script.
  function describe(thrown) {
    if (thrown === outputLimit) return { kind: 'output' };
    if (thrown instanceof InvalidReturn) return { kind: 'invalid', message: String(thrown.message) };
    try {
      if (thrown instanceof ErrorType) {
        return { kind: 'error', name: String(thrown.name), message: String(thrown.message), stack: String(thrown.stack) };
      }
      return { kind: 'value', message: String(thrown) };
    } catch (_) {
      return { kind: 'value', message: 'the code threw a value that cannot be shown as text' };
    }
  }

  // ---- Console ---------------------------------------------------------------

  var inspectLimit = 100;

  function quote(text) {
    return "'" + text.replace(/\\/g, '\\\\').replace(/'/g, "\\'").replace(/\n/g, '\\n') + "'";
  }

  // inspect renders a value for the console, in the spirit of Node's
  // util.inspect: bounded depth, long lists shortened, cycles marked.
  function inspect(value, depth, seen) {
    if (depth === undefined) depth = 4;
    switch (typeof value) {
      case 'string': return seen ? quote(value) : value;
      case 'number': return (value === 0 && 1 / value < 0) ? '-0' : String(value);
      case 'bigint': return String(value) + 'n';
      case 'boolean': return String(value);
      case 'undefined': return 'undefined';
      case 'symbol': return String(value);
      case 'function': return '[Function: ' + (value.name || '(anonymous)') + ']';
    }
    if (value === null) return 'null';
    seen = seen || [];
    for (var index = 0; index < seen.length; index++) {
      if (seen[index] === value) return '[Circular]';
    }
    if (value instanceof DateType) return isNaN(value.getTime()) ? 'Invalid Date' : value.toISOString();
    if (value instanceof ErrorType) return value.stack ? String(value.stack) : String(value.name) + ': ' + String(value.message);
    if (value instanceof RegExpType) return String(value);
    if (seen.length >= depth) return isArray(value) ? '[Array]' : '[Object]';
    var inner = seen.concat([value]);
    var parts = [];
    if (isArray(value)) {
      for (var position = 0; position < value.length && position < inspectLimit; position++) {
        parts.push(inspect(value[position], depth, inner));
      }
      if (value.length > inspectLimit) parts.push('... ' + (value.length - inspectLimit) + ' more items');
      return parts.length ? '[ ' + parts.join(', ') + ' ]' : '[]';
    }
    if (value instanceof MapType) {
      value.forEach(function (entry, key) {
        if (parts.length < inspectLimit) parts.push(inspect(key, depth, inner) + ' => ' + inspect(entry, depth, inner));
      });
      return 'Map(' + value.size + ') {' + (parts.length ? ' ' + parts.join(', ') + ' ' : '') + '}';
    }
    if (value instanceof SetType) {
      value.forEach(function (entry) {
        if (parts.length < inspectLimit) parts.push(inspect(entry, depth, inner));
      });
      return 'Set(' + value.size + ') {' + (parts.length ? ' ' + parts.join(', ') + ' ' : '') + '}';
    }
    var keys = ownKeys(value);
    for (var key = 0; key < keys.length && key < inspectLimit; key++) {
      var name = keys[key];
      var label = /^[A-Za-z_$][\w$]*$/.test(name) ? name : quote(name);
      parts.push(label + ': ' + inspect(value[name], depth, inner));
    }
    if (keys.length > inspectLimit) parts.push('... ' + (keys.length - inspectLimit) + ' more properties');
    return parts.length ? '{ ' + parts.join(', ') + ' }' : '{}';
  }

  // format joins a console call's arguments as Node's console does, including
  // the %s %d %i %f %j %o %O %c %% placeholders in a leading string.
  function format(args) {
    var text = '';
    var next = 0;
    if (typeof args[0] === 'string' && args.length > 1 && args[0].indexOf('%') >= 0) {
      next = 1;
      text = args[0].replace(/%[sdifjoOc%]/g, function (token) {
        if (token === '%%') return '%';
        if (next >= args.length) return token;
        var argument = args[next++];
        switch (token) {
          case '%s': return typeof argument === 'string' ? argument : inspect(argument, 2);
          case '%d': return typeof argument === 'object' ? 'NaN' : String(Number(argument));
          case '%i': return String(parseInt(argument, 10));
          case '%f': return String(parseFloat(argument));
          case '%j':
            try { return stringifyJSON(argument); } catch (_) { return '[Circular]'; }
          case '%c': return '';
          default: return inspect(argument, 4);
        }
      });
    }
    for (; next < args.length; next++) {
      text += (text === '' && next === 0 ? '' : ' ') + inspect(args[next], 4);
    }
    return text;
  }

  // ---- Allocation bounds -----------------------------------------------------

  // goja cannot interrupt a single built-in call, and the watchdog can only
  // interrupt, so a built-in that allocates or loops as far as a number tells
  // it could hold a core or the server's memory long past any limit:
  // `[...Array(2**26).keys()]` took 32 s and 6 GB, `new Uint8Array(2**30)`
  // allocated a gigabyte in one step. These built-ins refuse such a number up
  // front, with the RangeError an engine gives for a string that is too
  // long. Work that grows step by step in the code's own loops needs no guard:
  // the clock and the watchdog stop it between steps.
  var floor = Math.floor;
  var ReflectType = Reflect;
  var reflectConstruct = Reflect.construct;
  var setPrototypeOf = Object.setPrototypeOf;
  var getOwnPropertyNames = Object.getOwnPropertyNames;

  function lengthOf(value) {
    if (value === null || value === undefined) return 0;
    var length = Number(value.length);
    return length > 0 ? floor(length) : 0;
  }

  function tooLarge(what, size, limit) {
    return new RangeError(what + ' of ' + size + ' is more than the ' + limit + ' one call may handle here');
  }

  // replace swaps a built-in method for a checked one of the same name.
  function replace(owner, name, check) {
    var original = owner[name];
    if (typeof original !== 'function') return;
    var checked = function () {
      check(this, arguments);
      return apply(original, this, arguments);
    };
    defineProperty(checked, 'name', { value: original.name });
    defineProperty(checked, 'length', { value: original.length });
    defineProperty(owner, name, { value: checked, writable: true, configurable: true, enumerable: false });
  }

  // replaceConstructor swaps a global constructor for a checked function
  // sharing its prototype, so instanceof and subclassing still work.
  function replaceConstructor(name, check) {
    var original = global[name];
    if (typeof original !== 'function') return;
    function checked() {
      check(arguments);
      if (new.target === undefined) return apply(original, this, arguments);
      return reflectConstruct(original, arguments, new.target === checked ? original : new.target);
    }
    setPrototypeOf(checked, original);
    checked.prototype = original.prototype;
    defineProperty(checked, 'name', { value: name });
    defineProperty(global, name, { value: checked, writable: true, configurable: true, enumerable: false });
  }

  function boundAllocations(caps) {
    // Every array method walks its receiver's length, which a sparse array or
    // a plain object can set as high as it likes for free.
    getOwnPropertyNames(Array.prototype).forEach(function (name) {
      if (name === 'constructor') return;
      replace(Array.prototype, name, function (self) {
        var length = lengthOf(self);
        if (length > caps.elements) throw tooLarge('an array length', length, caps.elements);
      });
    });
    replace(Array.prototype, Symbol.iterator, function (self) {
      var length = lengthOf(self);
      if (length > caps.elements) throw tooLarge('an array length', length, caps.elements);
    });
    replace(Array, 'from', function (_, args) {
      var source = args[0];
      if (source !== null && source !== undefined && typeof source[Symbol.iterator] !== 'function') {
        var length = lengthOf(source);
        if (length > caps.elements) throw tooLarge('an array length', length, caps.elements);
      }
    });
    // An argument list is built whole before the call starts.
    function argumentList(list) {
      var length = lengthOf(list);
      if (length > caps.elements) throw tooLarge('an argument list', length, caps.elements);
    }
    replace(Function.prototype, 'apply', function (_, args) { argumentList(args[1]); });
    replace(ReflectType, 'apply', function (_, args) { argumentList(args[2]); });
    replace(ReflectType, 'construct', function (_, args) { argumentList(args[1]); });

    function characters(size) {
      if (size > caps.characters) throw tooLarge('a string length', size, caps.characters);
    }
    replace(String.prototype, 'repeat', function (self, args) {
      characters(String(self).length * (Number(args[0]) || 0));
    });
    replace(String.prototype, 'padStart', function (_, args) { characters(Number(args[0]) || 0); });
    replace(String.prototype, 'padEnd', function (_, args) { characters(Number(args[0]) || 0); });

    // Typed arrays and buffers allocate their whole size at once.
    function bytes(size) {
      if (size > caps.bytes) throw tooLarge('a buffer of bytes', size, caps.bytes);
    }
    replaceConstructor('ArrayBuffer', function (args) { bytes(Number(args[0]) || 0); });
    ['Int8Array', 'Uint8Array', 'Uint8ClampedArray', 'Int16Array', 'Uint16Array', 'Int32Array', 'Uint32Array',
      'Float32Array', 'Float64Array', 'BigInt64Array', 'BigUint64Array'].forEach(function (name) {
      var width = global[name] ? global[name].BYTES_PER_ELEMENT : 1;
      replaceConstructor(name, function (args) {
        var source = args[0];
        if (typeof source === 'number') {
          bytes(source * width);
        } else if (source !== null && typeof source === 'object' && !(source instanceof global.ArrayBuffer)) {
          bytes(lengthOf(source) * width);
        }
      });
    });
  }

  // ---- Roots -----------------------------------------------------------------

  function refusal(subject, advice) {
    return "this node's code " + subject + ', which this server does not run. ' + advice;
  }

  // hasPropertyEscape finds \p{ or \P{ in a pattern, skipping escaped
  // backslashes; it is the analyser's check, for patterns built at run time.
  function hasPropertyEscape(pattern) {
    for (var index = 0; index < pattern.length; index++) {
      if (pattern.charCodeAt(index) !== 92 || index + 1 >= pattern.length) continue;
      var next = pattern.charAt(index + 1);
      if ((next === 'p' || next === 'P') && pattern.charAt(index + 2) === '{') return true;
      index++;
    }
    return false;
  }

  function define(name, value) {
    defineProperty(global, name, { value: value, writable: true, configurable: true, enumerable: false });
  }

  // unavailable is a global that fails with its own name when it is read.
  function unavailable(name, message) {
    defineProperty(global, name, {
      get: function () { throw new ReferenceError(message); },
      set: function (value) { define(name, value); },
      configurable: true, enumerable: false,
    });
  }

  // install builds the Code node's roots over this execution's input, and
  // returns the one function the runner calls the user's code through.
  function install(snapshot, input, eachItem, host, factories) {
    var current = 0;
    for (var index = 0; index < input.length; index++) {
      remember(input[index], index);
      remember(input[index].json, index);
    }

    var inputRoot = {
      all: function () { return input; },
      first: function () { return input[0]; },
      last: function () { return input[input.length - 1]; },
    };
    defineProperty(inputRoot, 'item', { get: function () { return input[current]; }, enumerable: true });

    // Other nodes are fetched from the host when the code first names them,
    // so a body that never reads another node never pays for its data.
    var views = new Map();
    function nodeView(name) {
      name = String(name);
      var cached = views.get(name);
      if (cached) return cached;
      var raw = host.node(name);
      var data = raw === null || raw === undefined ? null : parseJSON(raw);
      var items = [];
      if (data) {
        for (var position = 0; position < data.items.length; position++) {
          items.push({ json: data.items[position] });
        }
      }
      function executed() {
        if (!data) throw new Error('node "' + name + '" has not run in this execution, or there is no node by that name');
      }
      function paired(itemIndex) {
        executed();
        var answer = host.pair(name, itemIndex);
        if (typeof answer === 'number') return items[answer];
        throw new Error('$("' + name + '").item: ' + answer);
      }
      var view = {
        all: function (branchIndex, runIndex) {
          if ((branchIndex !== undefined && branchIndex !== 0) || (runIndex !== undefined && runIndex !== 0)) {
            throw new Error(refusal('reads $("' + name + '").all() with a branch or run other than the first', snapshot.advice));
          }
          executed();
          return items;
        },
        first: function () { executed(); return items[0]; },
        last: function () { executed(); return items[items.length - 1]; },
        itemMatching: function (itemIndex) { return paired(itemIndex); },
        params: data ? data.params : undefined,
        isExecuted: !!data,
      };
      defineProperty(view, 'item', { get: function () { return paired(current); }, enumerable: true });
      views.set(name, view);
      return view;
    }

    define('$', function $(name) { return nodeView(name); });
    define('$node', new ProxyType({}, {
      get: function (_, name) {
        if (typeof name !== 'string') return undefined;
        var view = nodeView(name);
        if (!view.isExecuted) return undefined;
        var chosen;
        var answer = host.pair(name, current);
        var all = view.all();
        chosen = typeof answer === 'number' ? all[answer] : (all[current] || all[0]);
        return { json: chosen ? chosen.json : {}, parameter: view.params, params: view.params, isExecuted: true };
      },
    }));

    var execution = {
      id: snapshot.execution.id,
      mode: snapshot.execution.mode,
      resumeUrl: snapshot.execution.resumeUrl,
      approvalUrl: snapshot.execution.approvalUrl,
    };
    defineProperty(execution, 'customData', {
      get: function () { throw new Error(refusal('uses $execution.customData', snapshot.advice)); },
      enumerable: false,
    });
    define('$execution', execution);
    define('$workflow', snapshot.workflow);
    define('$env', snapshot.env);
    define('$vars', snapshot.vars);
    define('$runIndex', snapshot.runIndex);
    define('$nodeVersion', snapshot.nodeVersion);
    defineProperty(global, '$prevNode', {
      get: function () { throw new Error(refusal('uses $prevNode', snapshot.advice)); },
      configurable: true, enumerable: false,
    });
    [
      ['$jmespath', 'uses $jmespath'],
      ['$evaluateExpression', 'uses $evaluateExpression'],
      ['$getWorkflowStaticData', 'uses $getWorkflowStaticData'],
    ].forEach(function (entry) {
      define(entry[0], function () { throw new Error(refusal(entry[1], snapshot.advice)); });
    });
    define('$secrets', new ProxyType({}, {
      get: function () { throw new Error(refusal('uses $secrets', snapshot.advice)); },
    }));

    // A pattern the analyser could not see, because the code builds it at run
    // time, is checked here: goja accepts \p{…} under the u flag and matches
    // nothing, and a script must never run on with that wrong answer. Regular
    // expression literals were checked before the code ran.
    var construct = Reflect.construct;
    function checkPattern(pattern, flags) {
      var isRegExp = pattern instanceof RegExpType;
      var text = typeof pattern === 'string' ? pattern : isRegExp ? pattern.source : String(pattern);
      var mode = flags === undefined ? (isRegExp ? pattern.flags : '') : String(flags);
      ['v', 'd'].forEach(function (flag) {
        if (mode.indexOf(flag) >= 0) throw new Error(refusal("uses the regular-expression flag '" + flag + "'", snapshot.advice));
      });
      if (mode.indexOf('u') >= 0 && hasPropertyEscape(text)) {
        throw new Error(refusal('uses a regular expression with \\p{…} property escapes', snapshot.advice));
      }
    }
    // A plain function standing in for the constructor, sharing its
    // prototype, so `x instanceof RegExp` still holds; goja cannot use a
    // Proxy on the right of instanceof.
    function RegExpGuard(pattern, flags) {
      checkPattern(pattern, flags);
      if (new.target === undefined) return RegExpType(pattern, flags);
      return construct(RegExpType, [pattern, flags], new.target === RegExpGuard ? RegExpType : new.target);
    }
    Object.setPrototypeOf(RegExpGuard, RegExpType);
    RegExpGuard.prototype = RegExpType.prototype;
    defineProperty(RegExpGuard, 'name', { value: 'RegExp' });
    define('RegExp', RegExpGuard);

    if (eachItem) {
      unavailable('items', 'items is only available when the code runs once for all items; use $input.item or $json');
    } else {
      unavailable('$json', '$json is only available when the code runs once for each item; use $input.all() or $input.first()');
      unavailable('$itemIndex', '$itemIndex is only available when the code runs once for each item');
    }

    boundAllocations(snapshot.caps);

    var open = true;
    var console = {};
    ['log', 'info', 'warn', 'error', 'debug'].forEach(function (level) {
      console[level] = function () {
        if (!open) return;
        open = host.console(level, format(arguments)) !== false;
      };
    });
    console.trace = console.debug;
    console.dir = function (value) { console.log(value); };
    console.table = function (value) { console.log(value); };
    define('console', console);

    // this.helpers is n8n's host API for code. Until it is supported, each
    // helper refuses by name rather than being missing.
    var helpers = {};
    ['httpRequest', 'httpRequestWithAuthentication', 'request', 'getBinaryDataBuffer', 'prepareBinaryData'].forEach(function (name) {
      helpers[name] = function () { throw new Error(refusal('uses this.helpers.' + name, snapshot.advice)); };
    });
    var self = { helpers: helpers };

    // The runtime's own modules run now, before any user code, each seeing
    // the globals the ones before it defined.
    var libraries = new Map();
    function library(name) {
      if (!libraries.has(name)) libraries.set(name, host.library(name));
      return libraries.get(name);
    }
    var kit = {
      native: host.native,
      define: define,
      apply: apply,
      refusal: function (subject) { return refusal(subject, snapshot.advice); },
      inspect: function (value, depth) { return inspect(value, depth, []); },
      format: format,
      lengthOf: lengthOf,
      tooLarge: tooLarge,
      caps: snapshot.caps,
      library: library,
      timers: { start: host.timerStart, cancel: host.timerCancel },
      snapshot: snapshot,
      global: global,
    };
    var shipped = {};
    snapshot.modules.forEach(function (name) {
      var exported = factories[name](kit);
      if (exported !== undefined) shipped[name] = exported;
    });

    // require answers for the shipped modules and libraries only; there is no
    // npm and no module directory to look anything else up in.
    define('require', function require(request) {
      var name = String(request);
      if (name.slice(0, 5) === 'node:') name = name.slice(5);
      var module = snapshot.requirable[name];
      if (module !== undefined) return shipped[module];
      if (snapshot.libraries.indexOf(name) >= 0) return library(name);
      throw new Error(refusal('requires the module "' + name + '"', snapshot.advice));
    });
    // Libraries the analysis saw used are loaded now, where it costs the
    // code nothing, rather than on first use.
    snapshot.preload.forEach(library);

    return {
      run: function (body, itemIndex) {
        current = itemIndex;
        var args = eachItem ? [input[itemIndex].json, itemIndex, inputRoot] : [input, inputRoot];
        return apply(body, self, args);
      },
    };
  }

  return { normalise: normalise, stringify: stringify, describe: describe, install: install };
})()

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
  var ReferenceErrorType = ReferenceError;
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
  var ObjectType = Object;
  var ArrayType = Array;
  var NumberType = Number;
  var StringType = String;
  var SymbolType = Symbol;
  var getPrototypeOf = Object.getPrototypeOf;
  // The sort a comparator runs through, captured before any user code. goja's
  // is stable, as V8's is, so items a comparator calls equal keep the order
  // they arrived in.
  var arraySort = Array.prototype.sort;
  var arraySlice = Array.prototype.slice;

  // constructing tells a `new` call from a plain one, for a stand-in that
  // behaves differently in each. new.target alone cannot: goja passes a
  // constructor's new.target on to every plain call made inside it, so
  // `RegExp(p)` called from a constructor looks constructed. A genuine
  // construct call's receiver is the object being built, whose prototype is
  // new.target's.
  function constructing(receiver, target) {
    return target !== undefined && receiver !== null && (typeof receiver === 'object' || typeof receiver === 'function') &&
      getPrototypeOf(receiver) === target.prototype;
  }
  var getOwnPropertyDescriptor = Object.getOwnPropertyDescriptor;
  var hasOwnProperty = Object.prototype.hasOwnProperty;
  var functionToString = Function.prototype.toString;
  var errorToString = Error.prototype.toString;
  var stringIndexOf = String.prototype.indexOf;
  var stringSlice = String.prototype.slice;
  var charCodeAt = String.prototype.charCodeAt;
  var parseIntNumber = parseInt;
  var parseFloatNumber = parseFloat;
  var isFiniteNumber = isFinite;

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
      // n8n drops an item whose code returns null: `return $json.ok ? $json :
      // null` filters. Returning nothing at all is still a mistake.
      if (result === null) return [];
      if (result === undefined) {
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

  // jsonSize is a lower bound on the text JSON.stringify writes for one value
  // its replacer handed back, with the key and the comma or bracket that come
  // with it: exact for numbers, booleans, null and punctuation, and never more
  // than a string's UTF-8 length. A value stringify leaves out of an object
  // (undefined, a function, a symbol) costs nothing; in an array it is written
  // as null. A container counts one bracket, and each member the comma or
  // bracket before it, which together are every bracket and comma written.
  function jsonSize(holder, key, value) {
    var omitted = value === undefined || typeof value === 'function' || typeof value === 'symbol';
    var size = 0;
    if (key !== '') {
      if (isArray(holder)) {
        if (omitted) return 5;
        size = 1;
      } else {
        if (omitted) return 0;
        size = key.length + 4; // the comma or brace, the quoted key and its colon
      }
    }
    switch (typeof value) {
      case 'string': return size + value.length + 2;
      case 'number': return size + (isFiniteNumber(value) ? StringType(value).length : 4);
      case 'boolean': return size + (value ? 4 : 5);
      case 'object': return size + (value === null ? 4 : 1);
    }
    return size;
  }

  // stringify is JSON.stringify with a running size count, so an oversized
  // result stops early instead of being built whole before Go measures it.
  // Stopping on a lower bound never refuses output that would have fitted,
  // and Go checks the exact size of whatever gets through.
  function stringify(value, max) {
    var size = 0;
    return stringifyJSON(value, function (key, current) {
      size += jsonSize(this, key, current);
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
        var message = String(thrown.message);
        if (thrown instanceof ReferenceErrorType) message = withAdvice(thrown, message);
        return { kind: 'error', name: String(thrown.name), message: message, stack: String(thrown.stack) };
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

  function numberText(number) {
    return number === 0 && 1 / number < 0 ? '-0' : StringType(number);
  }

  // accessorLabel is how Node shows a property with a getter or a setter,
  // which inspect never calls; undefined for a plain value.
  function accessorLabel(object, key) {
    var descriptor = getOwnPropertyDescriptor(object, key);
    if (descriptor === undefined || apply(hasOwnProperty, descriptor, ['value'])) return undefined;
    if (descriptor.get !== undefined) return descriptor.set !== undefined ? '[Getter/Setter]' : '[Getter]';
    return descriptor.set !== undefined ? '[Setter]' : 'undefined';
  }

  // codeBody is the function the user's code runs in. How many lines the
  // code has is counted from that function's own text the first time an
  // error is shown: line 1 of the text is the wrapper's prelude and the line
  // after the code its trailer, so the count tells the code's own frames from
  // the wrapper's.
  var codeBody;
  var codeLines = -1;

  function linesOfCode() {
    if (codeLines < 0 && codeBody !== undefined) {
      var source = apply(functionToString, codeBody, []);
      var count = 0;
      for (var at = apply(stringIndexOf, source, ['\n']); at >= 0; at = apply(stringIndexOf, source, ['\n', at + 1])) count++;
      codeLines = count - 1;
    }
    return codeLines;
  }

  // sourceName is the file name the code has in a stack, as wrapper.go names
  // it.
  var sourceName = 'Code';
  var stackFrame = /^\s+at (?:(.+) \((.*)\)|(.*))$/;
  var framePlace = /^(.*):(\d+):(\d+)(?:\(\d+\))?$/;

  // errorText is an error as Node's console shows it: its stack, with the
  // code's own frames in the code's own line numbers, and without the frames
  // of the wrapper around the code or of the runtime and libraries under it.
  // A built-in's frame is kept when it sits between the code's own. An error
  // with no frames left is shown in brackets, as Node shows one with no
  // stack.
  function errorText(error) {
    var stack;
    try {
      stack = error.stack;
    } catch (_) {
      stack = undefined;
    }
    var written = stack ? StringType(stack) : apply(errorToString, error, []);
    var lines = written.split('\n');
    var head = [];
    var frames = [];
    var natives = [];
    var framed = false;
    var last = linesOfCode() + 1;
    for (var index = 0; index < lines.length; index++) {
      var frame = stackFrame.exec(lines[index]);
      if (frame === null) {
        if (!framed) head.push(lines[index]);
        continue;
      }
      framed = true;
      var name = frame[1];
      var place = name !== undefined ? frame[2] : frame[3];
      if (place === 'native') {
        natives.push('    at ' + name + ' (native)');
        continue;
      }
      var where = framePlace.exec(place);
      var line = where === null ? 0 : +where[2];
      if (where === null || where[1] !== sourceName || line < 2 || line > last) {
        natives = [];
        continue;
      }
      for (var pending = 0; pending < natives.length; pending++) frames.push(natives[pending]);
      natives = [];
      var at = sourceName + ':' + (line - 1) + ':' + where[3];
      frames.push('    at ' + (name !== undefined ? name + ' (' + at + ')' : at));
    }
    var headline = head.join('\n');
    return frames.length === 0 ? '[' + headline + ']' : headline + '\n' + frames.join('\n');
  }

  // inspect renders a value for the console, in the spirit of Node's
  // util.inspect: bounded depth, long lists shortened, cycles marked, and
  // getters shown rather than called.
  function inspect(value, depth, seen) {
    if (depth === undefined) depth = 4;
    switch (typeof value) {
      case 'string': return seen ? quote(value) : value;
      case 'number': return numberText(value);
      case 'bigint': return StringType(value) + 'n';
      case 'boolean': return StringType(value);
      case 'undefined': return 'undefined';
      case 'symbol': return StringType(value);
      case 'function': return '[Function: ' + (value.name || '(anonymous)') + ']';
    }
    if (value === null) return 'null';
    seen = seen || [];
    for (var index = 0; index < seen.length; index++) {
      if (seen[index] === value) return '[Circular]';
    }
    if (value instanceof DateType) return isNaN(value.getTime()) ? 'Invalid Date' : value.toISOString();
    if (value instanceof ErrorType) return errorText(value);
    if (value instanceof RegExpType) return StringType(value);
    if (seen.length >= depth) return isArray(value) ? '[Array]' : '[Object]';
    if (isView(value)) {
      var typed = -1;
      try {
        typed = typedLength(value);
      } catch (_) {
        typed = -1;
      }
      if (typed >= 0) return typedText(value, typed);
    }
    var inner = seen.concat([value]);
    var parts = [];
    var label;
    if (isArray(value)) {
      for (var position = 0; position < value.length && position < inspectLimit; position++) {
        label = accessorLabel(value, position);
        parts.push(label !== undefined ? label : inspect(value[position], depth, inner));
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
      label = accessorLabel(value, name);
      parts.push((/^[A-Za-z_$][\w$]*$/.test(name) ? name : quote(name)) + ': ' +
        (label !== undefined ? label : inspect(value[name], depth, inner)));
    }
    if (keys.length > inspectLimit) parts.push('... ' + (keys.length - inspectLimit) + ' more properties');
    return parts.length ? '{ ' + parts.join(', ') + ' }' : '{}';
  }

  // typedText is a typed array as Node shows one, and a Buffer as Node shows
  // a Buffer, reading no more of either than it shows: listing its keys
  // would make a string for every index.
  function typedText(value, length) {
    var parts = [];
    var index;
    if (bufferType !== undefined && value instanceof bufferType) {
      for (index = 0; index < length && index < 50; index++) {
        var byte = value[index];
        parts.push((byte < 16 ? '0' : '') + byte.toString(16));
      }
      return '<Buffer ' + parts.join(' ') + (length > 50 ? ' ... ' + (length - 50) + ' more bytes' : '') + '>';
    }
    for (index = 0; index < length && index < inspectLimit; index++) parts.push(inspect(value[index]));
    if (length > inspectLimit) parts.push('... ' + (length - inspectLimit) + ' more items');
    return apply(typedArrayTag, value, []) + '(' + length + ') ' + (parts.length ? '[ ' + parts.join(', ') + ' ]' : '[]');
  }

  // isCircular reports the TypeError stringify throws for a cycle, which
  // console's %j shows as [Circular], as Node's does.
  function isCircular(error) {
    return error instanceof TypeError && /circular/i.test(StringType(error.message));
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
          case '%d':
            if (typeof argument === 'bigint') return StringType(argument) + 'n';
            return typeof argument === 'symbol' ? 'NaN' : numberText(NumberType(argument));
          case '%i':
            if (typeof argument === 'bigint') return StringType(argument) + 'n';
            return typeof argument === 'symbol' ? 'NaN' : numberText(parseIntNumber(argument));
          case '%f':
            return typeof argument === 'symbol' ? 'NaN' : numberText(parseFloatNumber(argument));
          case '%j':
            try {
              return boundedStringify(argument);
            } catch (error) {
              if (isCircular(error)) return '[Circular]';
              // The console keeps a few kilobytes of a line; a text too large
              // to build at all is not worth failing the code over.
              if (error instanceof RangeError && / one call may handle here$/.test(StringType(error.message))) return '[JSON too large to show]';
              throw error;
            }
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
  //
  // Each check reads what the built-in will read, once, and where it can the
  // built-in is handed what the check read: a getter, a valueOf or a Proxy
  // could otherwise answer the check with one number and the built-in with
  // another. A length that cannot be read without running the code's own
  // functions is refused rather than read twice.
  //
  // These are defence in depth. Where closing a gap would break ordinary code
  // the gap is left: syntax that allocates in one step (spreading a huge
  // typed array into an object literal), and built-ins that collect every
  // match before returning (a global match, split or replace over a string
  // of tens of millions of characters), are bounded only by the input caps.
  var floor = Math.floor;
  var reflectConstruct = Reflect.construct;
  var setPrototypeOf = Object.setPrototypeOf;
  var getOwnPropertyNames = Object.getOwnPropertyNames;
  var isView = ArrayBuffer.isView;
  var ArrayBufferType = ArrayBuffer;
  var arrayBufferByteLength = getOwnPropertyDescriptor(ArrayBuffer.prototype, 'byteLength').get;
  var TypedArray = getPrototypeOf(Int8Array);
  var typedArrayLength = getOwnPropertyDescriptor(TypedArray.prototype, 'length').get;
  var typedArrayTag = getOwnPropertyDescriptor(TypedArray.prototype, SymbolType.toStringTag).get;
  var nativeJoin = Array.prototype.join;
  var stringValueOf = String.prototype.valueOf;
  var numberValueOf = Number.prototype.valueOf;
  var booleanValueOf = Boolean.prototype.valueOf;
  var bigintValueOf = typeof BigInt === 'function' ? BigInt.prototype.valueOf : undefined;
  var isRawJSON = typeof JSON.isRawJSON === 'function' ? JSON.isRawJSON : undefined;
  var setHas = Set.prototype.has;
  var setAdd = Set.prototype.add;
  var setDelete = Set.prototype.delete;
  var maxLength = 9007199254740991;

  // limits and advice are the run's, and bufferType the Buffer class, set
  // when the bounds are installed.
  var limits = { elements: 0, characters: 0, bytes: 0 };
  var advice = '';
  var bufferType;

  // proxies are the Proxies the code made. A built-in reads a Proxy's length
  // through the code's own handler, which can tell it something other than
  // what it told the check, so a Proxy is refused where its length matters.
  // A Set, not a WeakSet: goja backs a WeakSet with a Go weak pointer per
  // object asked about, which made every array method call cost a weak
  // handle. The VM lives for one execution, so holding its Proxies costs
  // nothing that outlives it.
  var proxies = new SetType();

  function isProxy(value) {
    return apply(setHas, proxies, [value]);
  }

  function tooLarge(what, size, limit) {
    return new RangeError(what + ' of ' + size + ' is more than the ' + limit + ' one call may handle here');
  }

  function unsupported(subject) {
    return new ErrorType(refusal(subject, advice));
  }

  // toLength is ToLength. Its ToNumber throws for a Symbol or a BigInt, as
  // the built-in's does.
  function toLength(value) {
    var number = +value;
    if (!(number > 0)) return 0;
    return number > maxLength ? maxLength : floor(number);
  }

  // text is ToString, which, unlike String(), refuses a Symbol.
  function text(value) {
    if (typeof value === 'string') return value;
    if (typeof value === 'symbol') throw new TypeError('Cannot convert a Symbol value to a string');
    return StringType(value);
  }

  // hasSlot reports whether value is a Number, String, Boolean or BigInt
  // object, by asking that type's own valueOf, which runs none of the code.
  function hasSlot(valueOf, value) {
    try {
      apply(valueOf, value, []);
      return true;
    } catch (_) {
      return false;
    }
  }

  // lengthOf is the length a built-in reads from an array-like, read as the
  // built-in reads it but without running any of the code's own functions:
  // an array's own length, another object's own or inherited data property,
  // or a typed array's. A Proxy, a length that is a getter and a length that
  // is an object (whose valueOf the built-in would call again) are refused.
  function lengthOf(value) {
    if (value === null || value === undefined) return 0;
    if (typeof value === 'string') return value.length;
    var object = typeof value === 'object' || typeof value === 'function' ? value : ObjectType(value);
    if (isArray(object) && !isProxy(object)) return object.length;
    for (var holder = object; holder !== null; holder = getPrototypeOf(holder)) {
      if (isProxy(holder)) throw unsupported('passes a Proxy to a built-in that reads its length');
      var descriptor = getOwnPropertyDescriptor(holder, 'length');
      if (descriptor === undefined) continue;
      if (apply(hasOwnProperty, descriptor, ['value'])) {
        var length = descriptor.value;
        if (length !== null && (typeof length === 'object' || typeof length === 'function')) {
          throw unsupported('passes a built-in an object whose length is itself an object');
        }
        return toLength(length);
      }
      if (descriptor.get === typedArrayLength) return apply(typedArrayLength, object, []);
      throw unsupported('passes a built-in an object whose length is a getter');
    }
    return 0;
  }

  function elements(length) {
    if (length > limits.elements) throw tooLarge('an array length', length, limits.elements);
    return length;
  }

  function characters(size) {
    if (size > limits.characters) throw tooLarge('a string length', size, limits.characters);
    return size;
  }

  function bytes(size) {
    if (size > limits.bytes) throw tooLarge('a buffer of bytes', size, limits.bytes);
  }

  // standIns maps each replaced built-in to the checked function standing in
  // for it, so a built-in reachable by two names (Array.prototype.values is
  // Array.prototype[Symbol.iterator]) stays one function under both.
  var standIns = new MapType();

  // replace swaps a built-in method for the checked one make builds around
  // it. make builds it as a method, so, like the built-in, it cannot be
  // called with new.
  function replace(owner, key, make) {
    // Only methods are named here, never an accessor, so reading one runs
    // nothing; and no user code has run yet.
    var original = owner[key];
    if (typeof original !== 'function') return;
    var checked = standIns.get(original);
    if (checked === undefined) {
      checked = make(original);
      defineProperty(checked, 'name', { value: original.name });
      defineProperty(checked, 'length', { value: original.length });
      standIns.set(original, checked);
    }
    defineProperty(owner, key, { value: checked, writable: true, configurable: true, enumerable: false });
  }

  // receiverBounded bounds a method that walks its receiver's length, which
  // a sparse array or a plain object can set as high as it likes for free.
  function receiverBounded(original) {
    return ({
      method() {
        var self = this;
        if (!isArray(self) || isProxy(self) || self.length > limits.elements) elements(lengthOf(self));
        return apply(original, self, arguments);
      },
    }).method;
  }

  // spreadShare is how many elements a value adds to a concat, counted high:
  // concat asks an object whether to spread it after the check has, and
  // could be told yes, so every object counts at its length.
  function spreadShare(value) {
    if (value === null || (typeof value !== 'object' && typeof value !== 'function')) return 1;
    var length = lengthOf(value);
    return length > 1 ? length : 1;
  }

  function concatBounded(original) {
    return ({
      concat() {
        var total = spreadShare(this);
        for (var index = 0; index < arguments.length; index++) total += spreadShare(arguments[index]);
        elements(total);
        return apply(original, this, arguments);
      },
    }).concat;
  }

  // joining are the receivers being joined, so a list that contains itself
  // joins as '' where it recurs, as goja's and V8's do.
  var joining = new SetType();

  // joined builds a join in JavaScript: each element becomes a string once,
  // counted as it is, so a long separator or one long string repeated a
  // million times fails before the text is built, and the built-in join is
  // only ever handed strings.
  function joined(object, length, glue, piece) {
    var total = characters(length > 1 ? glue.length * (length - 1) : 0);
    var parts = [];
    for (var index = 0; index < length; index++) {
      var element = object[index];
      var part = element === undefined || element === null ? '' : piece(element);
      total = characters(total + part.length);
      parts[index] = part;
    }
    return apply(nativeJoin, parts, [glue]);
  }

  function joiner(measure, separated) {
    return function (original) {
      return ({
        join(separator) {
          if (this === null || this === undefined) return apply(original, this, arguments);
          var object = typeof this === 'object' || typeof this === 'function' ? this : ObjectType(this);
          if (apply(setHas, joining, [object])) return '';
          var length = elements(measure(object));
          var glue = ',';
          var piece = text;
          if (separated) {
            if (separator !== undefined) glue = text(separator);
          } else {
            // toLocaleString passes its locales and options on to each
            // element, as Node's does.
            var passed = arguments;
            piece = function (element) {
              var method = element.toLocaleString;
              if (typeof method !== 'function') throw new TypeError('toLocaleString is not a function');
              return text(apply(method, element, passed));
            };
          }
          apply(setAdd, joining, [object]);
          try {
            return joined(object, length, glue, piece);
          } finally {
            apply(setDelete, joining, [object]);
          }
        },
      }).join;
    };
  }

  function typedLength(object) {
    return apply(typedArrayLength, object, []);
  }

  // listBounded bounds an argument list, which is built whole before the
  // call starts.
  function listBounded(position) {
    return function (original) {
      return ({
        method() {
          var list = arguments[position];
          if (list !== null && list !== undefined) {
            var length = lengthOf(list);
            if (length > limits.elements) throw tooLarge('an argument list', length, limits.elements);
          }
          return apply(original, this, arguments);
        },
      }).method;
    };
  }

  // ownKeyCount bounds the keys Object.keys and its kind list: a typed array
  // or a string has one for every index, and a typed array can have far more
  // indexes than an array may elements.
  function ownKeyCount(value) {
    var count = 0;
    if (typeof value === 'string') {
      count = value.length;
    } else if (value !== null && typeof value === 'object') {
      if (isView(value)) {
        try { count = typedLength(value); } catch (_) { count = 0; }
      } else if (value instanceof StringType) {
        try { count = apply(stringValueOf, value, []).length; } catch (_) { count = 0; }
      }
    }
    if (count > limits.elements) throw tooLarge('a list of keys', count, limits.elements);
  }

  function keysBounded(position) {
    return function (original) {
      return ({
        method() {
          if (position >= 0) {
            ownKeyCount(arguments[position]);
          } else {
            for (var index = 1; index < arguments.length; index++) ownKeyCount(arguments[index]);
          }
          return apply(original, this, arguments);
        },
      }).method;
    };
  }

  function receiverText(value) {
    return typeof value === 'string' ? value : text(value);
  }

  // substitute expands a replacement template for one match, as
  // String.prototype.replace does (GetSubstitution), and fails as soon as the
  // expansion is longer than allowed.
  function substitute(template, matched, subject, position, captures, groups, allowed) {
    var out = '';
    var count = template.length;
    var index = 0;
    while (index < count) {
      var dollar = apply(stringIndexOf, template, ['$', index]);
      if (dollar < 0) dollar = count;
      if (dollar > index) {
        out += apply(stringSlice, template, [index, dollar]);
        if (out.length > allowed) throw tooLarge('a string length', out.length - allowed + limits.characters, limits.characters);
      }
      if (dollar >= count) break;
      var code = dollar + 1 < count ? apply(charCodeAt, template, [dollar + 1]) : -1;
      var piece = '$';
      var width = 1;
      if (code === 36) { // $$
        width = 2;
      } else if (code === 38) { // $&
        piece = matched;
        width = 2;
      } else if (code === 96) { // $`
        piece = apply(stringSlice, subject, [0, position]);
        width = 2;
      } else if (code === 39) { // $'
        var tail = position + matched.length;
        piece = tail >= subject.length ? '' : apply(stringSlice, subject, [tail]);
        width = 2;
      } else if (code >= 48 && code <= 57) { // $n and $nn
        var second = dollar + 2 < count ? apply(charCodeAt, template, [dollar + 2]) : -1;
        var digits = second >= 48 && second <= 57 ? 2 : 1;
        var number = digits === 2 ? (code - 48) * 10 + (second - 48) : code - 48;
        if (digits === 2 && number > captures.length) {
          digits = 1;
          number = code - 48;
        }
        width = 1 + digits;
        if (number >= 1 && number <= captures.length) {
          piece = captures[number - 1];
          if (piece === undefined) piece = '';
        } else {
          piece = apply(stringSlice, template, [dollar, dollar + width]);
        }
      } else if (code === 60) { // $<name>
        var close = groups === undefined ? -1 : apply(stringIndexOf, template, ['>', dollar]);
        width = 2;
        piece = '$<';
        if (close >= 0) {
          var capture = groups[apply(stringSlice, template, [dollar + 2, close])];
          piece = capture === undefined ? '' : text(capture);
          width = close + 1 - dollar;
        }
      }
      out += piece;
      if (out.length > allowed) throw tooLarge('a string length', out.length - allowed + limits.characters, limits.characters);
      index = dollar + width;
    }
    return out;
  }

  // substitution is a replacer function that expands a template exactly as
  // replace would, counting how far the result has grown.
  function substitution(template, subjectLength) {
    var grown = 0;
    return function (matched) {
      var last = arguments.length - 1;
      var groups;
      if (typeof arguments[last] !== 'string') {
        groups = arguments[last];
        last--;
      }
      var subject = arguments[last];
      var position = arguments[last - 1];
      var captures = [];
      for (var index = 1; index < last - 1; index++) captures[index - 1] = arguments[index];
      var allowed = limits.characters - subjectLength - grown + matched.length;
      var expanded = substitute(template, matched, subject, position, captures, groups, allowed);
      grown += expanded.length - matched.length;
      return expanded;
    };
  }

  // replacedBound is the most text replacing `matches` matches in a subject
  // can produce with a template, when every match is a piece of the subject.
  // $` and $' can each stand for the whole subject at every match. Any other
  // reference stands for part of one match, and matches do not overlap, so
  // each adds up to the subject once however many matches there are.
  function replacedBound(subjectLength, matches, template) {
    var positional = 0;
    var other = 0;
    for (var at = apply(stringIndexOf, template, ['$']); at >= 0; at = apply(stringIndexOf, template, ['$', at + 1])) {
      var next = apply(charCodeAt, template, [at + 1]);
      if (next === 96 || next === 39) positional++;
      else other++;
    }
    return subjectLength + matches * template.length + other * subjectLength + positional * matches * subjectLength;
  }

  // replaceWith hands the built-in a template when the result cannot outgrow
  // the bound, which is nearly always, and otherwise a replacer function
  // that builds the same text and counts it. Matches that need not be pieces
  // of the subject, from a regular expression with its own exec, always go
  // through the replacer.
  function replaceWith(original, receiver, pattern, subject, template, matches, piecesOfSubject) {
    if (piecesOfSubject && replacedBound(subject.length, matches, template) <= limits.characters) {
      return apply(original, receiver, [pattern, template]);
    }
    return apply(original, receiver, [pattern, substitution(template, subject.length)]);
  }

  var regExpExec = RegExp.prototype.exec;

  // standardExec reports whether a regular expression matches with the
  // built-in exec, found without running any of the code's functions.
  function standardExec(rx) {
    for (var holder = rx; holder !== null && (typeof holder === 'object' || typeof holder === 'function'); holder = getPrototypeOf(holder)) {
      if (isProxy(holder)) return false;
      var descriptor = getOwnPropertyDescriptor(holder, 'exec');
      if (descriptor !== undefined) return descriptor.value === regExpExec;
    }
    return false;
  }

  function stringReplaceBounded(all) {
    return function (original) {
      return ({
        replace(searchValue, replaceValue) {
          // A regular expression, or any object with its own Symbol.replace,
          // does the replacing itself; RegExp's is bounded below.
          if (this === null || this === undefined || typeof replaceValue === 'function' ||
              (searchValue !== null && (typeof searchValue === 'object' || typeof searchValue === 'function'))) {
            return apply(original, this, arguments);
          }
          var subject = receiverText(this);
          var pattern = text(searchValue);
          var template = text(replaceValue);
          var matches = 1;
          if (all) matches = pattern.length === 0 ? subject.length + 1 : floor(subject.length / pattern.length);
          return replaceWith(original, subject, searchValue, subject, template, matches, true);
        },
      }).replace;
    };
  }

  function regExpReplaceBounded(original) {
    return ({
      replace(string, replaceValue) {
        var subject = text(string);
        if (typeof replaceValue === 'function') return apply(original, this, [subject, replaceValue]);
        // Counted as global, whatever the flags say: they are read later, and
        // could say something else then.
        return replaceWith(original, this, subject, subject, text(replaceValue), subject.length + 1, standardExec(this));
      },
    }).replace;
  }

  // propertyList is JSON.stringify's allowlist of keys, read once from an
  // array replacer, as stringify reads it.
  function propertyList(replacer) {
    var list = [];
    var seen = new SetType();
    var length = toLength(replacer.length);
    for (var index = 0; index < length; index++) {
      var entry = replacer[index];
      var key;
      if (typeof entry === 'string') {
        key = entry;
      } else if (typeof entry === 'number' ||
          (entry !== null && typeof entry === 'object' && (hasSlot(stringValueOf, entry) || hasSlot(numberValueOf, entry)))) {
        key = text(entry);
      } else {
        continue;
      }
      if (!apply(setHas, seen, [key])) {
        apply(setAdd, seen, [key]);
        list.push(key);
      }
    }
    return list;
  }

  var arrayIndex = /^(?:0|[1-9][0-9]*)$/;

  // project is an object as stringify writes it under a property list: only
  // the listed keys, in the list's order. It replaces the list, because a
  // replacer function and a list cannot both be handed to the built-in.
  function project(value, list) {
    if (value === null || typeof value !== 'object' || isArray(value) ||
        (isRawJSON !== undefined && isRawJSON(value)) || hasSlot(numberValueOf, value) ||
        hasSlot(stringValueOf, value) || hasSlot(booleanValueOf, value) ||
        (bigintValueOf !== undefined && hasSlot(bigintValueOf, value))) {
      return value;
    }
    var copy = {};
    var indexed = false;
    for (var index = 0; index < list.length; index++) {
      var key = list[index];
      defineProperty(copy, key, { value: value[key], writable: true, enumerable: true, configurable: true });
      if (arrayIndex.test(key) && +key < 4294967295) indexed = true;
    }
    if (!indexed) return copy;
    // An object lists index keys first, whatever order they were added in;
    // the list keeps its own order, so the copy reports it.
    return new ProxyType(copy, { ownKeys: function () { return list.slice(); } });
  }

  var jsonToken = /["{}[\],:]/g;

  // indented lays compact JSON out as JSON.stringify lays it out with a gap,
  // counting as it goes. goja's own layout keeps the indentation of an empty
  // array or object for everything written after it; Node's does not.
  function indented(compact, gap) {
    var parts = [];
    var size = 0;
    var depth = 0;
    var indents = [''];
    var start = 0;
    function grow(count) {
      size += count;
      if (size > limits.characters) throw tooLarge('a string length', size, limits.characters);
    }
    function line() {
      if (indents[depth] === undefined) indents[depth] = indents[depth - 1] + gap;
      grow(1 + indents[depth].length);
      parts.push('\n' + indents[depth]);
    }
    jsonToken.lastIndex = 0;
    for (var match = jsonToken.exec(compact); match !== null; match = jsonToken.exec(compact)) {
      var at = match.index;
      var token = match[0];
      if (token === '"') {
        // A string is copied as it is: find its closing quote, the first
        // one not escaped by an odd run of backslashes.
        var end = at;
        for (;;) {
          end = apply(stringIndexOf, compact, ['"', end + 1]);
          if (end < 0) end = compact.length;
          var slashes = 0;
          while (apply(charCodeAt, compact, [end - 1 - slashes]) === 92) slashes++;
          if (slashes % 2 === 0) break;
        }
        jsonToken.lastIndex = end + 1;
        continue;
      }
      if (at > start) {
        grow(at - start);
        parts.push(apply(stringSlice, compact, [start, at]));
      }
      start = at + 1;
      if (token === '{' || token === '[') {
        var close = token === '{' ? '}' : ']';
        if (apply(stringSlice, compact, [at + 1, at + 2]) === close) {
          grow(2);
          parts.push(token + close);
          start = at + 2;
          jsonToken.lastIndex = start;
          continue;
        }
        grow(1);
        parts.push(token);
        depth++;
        line();
      } else if (token === '}' || token === ']') {
        depth--;
        line();
        grow(1);
        parts.push(token);
      } else if (token === ',') {
        grow(1);
        parts.push(',');
        line();
      } else {
        grow(2);
        parts.push(': ');
      }
    }
    if (start < compact.length) parts.push(apply(stringSlice, compact, [start]));
    return apply(nativeJoin, parts, ['']);
  }

  // boundedStringify is JSON.stringify as the code calls it, with the text
  // counted as it is written, so a structure that repeats one large part
  // many times fails before the text is built. Indentation is laid out
  // afterwards, from the compact text, and counted there.
  function boundedStringify(value, replacer, space) {
    var call = typeof replacer === 'function' ? replacer : undefined;
    var list = call === undefined && isArray(replacer) ? propertyList(replacer) : undefined;
    if (space !== null && typeof space === 'object') {
      if (hasSlot(numberValueOf, space)) space = +space;
      else if (hasSlot(stringValueOf, space)) space = text(space);
    }
    var gap = '';
    if (typeof space === 'number') {
      for (var width = space >= 10 ? 10 : floor(space); gap.length < width;) gap += ' ';
    } else if (typeof space === 'string') {
      gap = apply(stringSlice, space, [0, 10]);
    }
    var size = 0;
    var compact = apply(stringifyJSON, JSON, [value, function (key, current) {
      if (call !== undefined) current = apply(call, this, [key, current]);
      else if (list !== undefined) current = project(current, list);
      size += jsonSize(this, key, current);
      if (size > limits.characters) throw tooLarge('a string length', size, limits.characters);
      return current;
    }]);
    return gap === '' || typeof compact !== 'string' ? compact : indented(compact, gap);
  }

  // standIn replaces a global constructor with a checked function, and
  // leaves the original out of reach: the stand-in has the original's own
  // statics and [[Prototype]], and the prototype's constructor names the
  // stand-in, so neither Object.getPrototypeOf(Uint8Array) nor x.constructor
  // hands the code the unchecked original. check returns the arguments to
  // pass on, coerced where a check read them.
  function standIn(name, statics, check) {
    var original = global[name];
    if (typeof original !== 'function') return undefined;
    function checked() {
      var args = check(arguments);
      if (!constructing(this, new.target)) return apply(original, this, args);
      return reflectConstruct(original, args, new.target === checked ? original : new.target);
    }
    adopt(checked, original, name, statics);
    define(name, checked);
    return checked;
  }

  // adopt gives a stand-in its original's place: [[Prototype]], prototype,
  // name, length, the own statics named (goja gives ArrayBuffer isView and a
  // species, and each typed array BYTES_PER_ELEMENT), and the prototype's
  // constructor. The statics are named rather than listed from the original
  // because listing them makes goja build every one, on every run.
  function adopt(checked, original, name, statics) {
    setPrototypeOf(checked, getPrototypeOf(original));
    defineProperty(checked, 'prototype', { value: original.prototype, writable: false });
    defineProperty(checked, 'name', { value: name });
    defineProperty(checked, 'length', { value: original.length });
    for (var index = 0; index < statics.length; index++) {
      defineProperty(checked, statics[index], getOwnPropertyDescriptor(original, statics[index]));
    }
    defineProperty(original.prototype, 'constructor', { value: checked, writable: true, configurable: true, enumerable: false });
  }

  function isArrayBuffer(value) {
    if (!(value instanceof ArrayBufferType)) return false;
    try {
      apply(arrayBufferByteLength, value, []);
      return true;
    } catch (_) {
      return false;
    }
  }

  function boundAllocations(caps, refusalAdvice) {
    limits = caps;
    advice = refusalAdvice;

    // Arrays. push, pop and at do not walk the receiver, and toString is a
    // join.
    var arrayPrototype = ArrayType.prototype;
    var unbounded = { constructor: true, push: true, pop: true, at: true, toString: true };
    getOwnPropertyNames(arrayPrototype).forEach(function (name) {
      if (apply(hasOwnProperty, unbounded, [name])) return;
      if (name === 'concat') replace(arrayPrototype, name, concatBounded);
      else if (name === 'join') replace(arrayPrototype, name, joiner(lengthOf, true));
      else if (name === 'toLocaleString') replace(arrayPrototype, name, joiner(lengthOf, false));
      else replace(arrayPrototype, name, receiverBounded);
    });
    replace(arrayPrototype, SymbolType.iterator, receiverBounded);
    replace(ArrayType, 'from', function (original) {
      return ({
        from(items) {
          if (items !== null && items !== undefined) elements(lengthOf(items));
          return apply(original, this, arguments);
        },
      }).from;
    });
    replace(Function.prototype, 'apply', listBounded(1));
    replace(Reflect, 'apply', listBounded(2));
    replace(Reflect, 'construct', listBounded(1));

    // Typed arrays hold up to MaxBytesPerCall elements, which is more than an
    // array may: what turns each into a value is bounded as an array is.
    var typedPrototype = TypedArray.prototype;
    ['keys', 'values', 'entries', SymbolType.iterator].forEach(function (key) {
      replace(typedPrototype, key, function (original) {
        return ({
          method() {
            elements(typedLength(this));
            return apply(original, this, arguments);
          },
        }).method;
      });
    });
    replace(typedPrototype, 'join', joiner(typedLength, true));
    replace(typedPrototype, 'toLocaleString', joiner(typedLength, false));
    ['sort', 'toSorted'].forEach(function (name) {
      replace(typedPrototype, name, function (original) {
        return ({
          sort(compare) {
            if (compare === undefined) elements(typedLength(this));
            return apply(original, this, arguments);
          },
        }).sort;
      });
    });

    // Keys.
    replace(ObjectType, 'keys', keysBounded(0));
    replace(ObjectType, 'values', keysBounded(0));
    replace(ObjectType, 'entries', keysBounded(0));
    replace(ObjectType, 'getOwnPropertyNames', keysBounded(0));
    replace(ObjectType, 'getOwnPropertyDescriptors', keysBounded(0));
    replace(ObjectType, 'assign', keysBounded(-1));
    replace(Reflect, 'ownKeys', keysBounded(0));

    // Strings.
    var stringPrototype = StringType.prototype;
    replace(stringPrototype, 'repeat', function (original) {
      return ({
        repeat(count) {
          if (this === null || this === undefined) return apply(original, this, arguments);
          var self = receiverText(this);
          var times = +count;
          if (times >= 1 && times !== Infinity) characters(self.length * floor(times));
          return apply(original, self, [times]);
        },
      }).repeat;
    });
    ['padStart', 'padEnd'].forEach(function (name) {
      replace(stringPrototype, name, function (original) {
        return ({
          pad(maxLength, fillString) {
            if (this === null || this === undefined) return apply(original, this, arguments);
            var self = receiverText(this);
            var target = toLength(maxLength);
            var fill = fillString === undefined ? ' ' : text(fillString);
            if (target > self.length && fill !== '') characters(target);
            return apply(original, self, [target, fill]);
          },
        }).pad;
      });
    });
    replace(stringPrototype, 'concat', function (original) {
      return ({
        concat() {
          if (this === null || this === undefined) return apply(original, this, arguments);
          var self = receiverText(this);
          var total = self.length;
          var parts = [];
          for (var index = 0; index < arguments.length; index++) {
            parts[index] = text(arguments[index]);
            total = characters(total + parts[index].length);
          }
          return apply(original, self, parts);
        },
      }).concat;
    });
    replace(stringPrototype, SymbolType.iterator, function (original) {
      return ({
        method() {
          if (this === null || this === undefined) return apply(original, this, arguments);
          var self = receiverText(this);
          if (self.length > limits.elements) throw tooLarge('a string to iterate over, with a length', self.length, limits.elements);
          return apply(original, self, []);
        },
      }).method;
    });
    replace(stringPrototype, 'replace', stringReplaceBounded(false));
    replace(stringPrototype, 'replaceAll', stringReplaceBounded(true));
    replace(RegExpType.prototype, SymbolType.replace, regExpReplaceBounded);
    replace(JSON, 'stringify', function () {
      return ({
        stringify(value, replacer, space) {
          return boundedStringify(value, replacer, space);
        },
      }).stringify;
    });

    // Constructors that allocate their whole size at once. A length that is
    // not an object is coerced once, and the built-in is handed the number.
    standIn('ArrayBuffer', ['isView', SymbolType.species], function (args) {
      if (args.length === 0) return args;
      var size = +args[0];
      bytes(size > 0 ? floor(size) : 0);
      var passed = [size];
      for (var index = 1; index < args.length; index++) passed[index] = args[index];
      return passed;
    });
    var originalUint8Array = global.Uint8Array;
    var typedStatics = ['BYTES_PER_ELEMENT'];
    var checkedUint8Array;
    ['Int8Array', 'Uint8Array', 'Uint8ClampedArray', 'Int16Array', 'Uint16Array', 'Int32Array', 'Uint32Array',
      'Float32Array', 'Float64Array', 'BigInt64Array', 'BigUint64Array'].forEach(function (name) {
      var width = global[name] ? global[name].BYTES_PER_ELEMENT : 1;
      var checked = standIn(name, typedStatics, function (args) {
        if (args.length === 0) return args;
        var source = args[0];
        if (source === null || (typeof source !== 'object' && typeof source !== 'function')) {
          var length = +source;
          bytes((length > 0 ? floor(length) : 0) * width);
          return [length];
        }
        // A view over a buffer allocates nothing; anything else is copied.
        if (!isArrayBuffer(source)) bytes(lengthOf(source) * width);
        return args;
      });
      if (name === 'Uint8Array') checkedUint8Array = checked;
    });
    // Buffer extends Uint8Array; its [[Prototype]] would otherwise still be
    // the unchecked original.
    bufferType = typeof global.Buffer === 'function' ? global.Buffer : undefined;
    if (bufferType !== undefined && checkedUint8Array !== undefined && getPrototypeOf(bufferType) === originalUint8Array) {
      setPrototypeOf(bufferType, checkedUint8Array);
    }

    // Every Proxy the code makes is remembered, so lengthOf can refuse one.
    var revocable = ProxyType.revocable;
    defineProperty(ProxyType, 'revocable', {
      value: ({
        revocable(target, handler) {
          var pair = revocable(target, handler);
          apply(setAdd, proxies, [pair.proxy]);
          return pair;
        },
      }).revocable,
      writable: true, configurable: true, enumerable: false,
    });
    define('Proxy', new ProxyType(ProxyType, {
      construct: function (target, args, newTarget) {
        var proxy = reflectConstruct(target, args, newTarget);
        apply(setAdd, proxies, [proxy]);
        return proxy;
      },
    }));
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
  // otherMode says what to use instead of a root that belongs to the other
  // mode. The roots are left undefined, as in n8n, so `typeof items` is
  // 'undefined' in per-item code; only an uncaught "is not defined" for one of
  // them is told what to use instead.
  var otherMode = {};

  function unavailable(name, message) {
    otherMode[name] = message;
  }

  // withAdvice is a thrown ReferenceError for another mode's root, reworded
  // to say what to use instead.
  function withAdvice(thrown, message) {
    var match = /^(\$?\w+) is not defined$/.exec(message);
    return match !== null && apply(hasOwnProperty, otherMode, [match[1]]) ? otherMode[match[1]] : message;
  }

  // binaryOf is $binary for an item: a copy of each of its files' metadata,
  // so changing it changes nothing the item holds, as in n8n. An item with no
  // files has an empty one, and no item has none at all.
  function binaryOf(item) {
    if (item === undefined) return undefined;
    var copy = {};
    var files = item.binary;
    if (!isObject(files)) return copy;
    var names = ownKeys(files);
    for (var index = 0; index < names.length; index++) {
      var file = files[names[index]];
      if (!isObject(file)) continue;
      var entry = {};
      var fields = ownKeys(file);
      for (var field = 0; field < fields.length; field++) {
        if (fields[field] !== 'data') entry[fields[field]] = file[fields[field]];
      }
      copy[names[index]] = entry;
    }
    return copy;
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
    // Both describe the node before this one, which the runner cannot name,
    // as with $prevNode: its settings, and whether a Loop Over Items node has
    // items left. They refuse by name rather than read as undefined.
    ['params', 'context'].forEach(function (name) {
      defineProperty(inputRoot, name, {
        get: function () { throw new Error(refusal('uses $input.' + name, snapshot.advice)); },
        enumerable: false,
      });
    });

    // Other nodes are fetched from the host when the code first names them,
    // so a body that never reads another node never pays for its data.
    var views = new Map();
    // readers read one output of a node's latest run, by name, for .all()
    // and $items.
    var readers = new Map();
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
      // The node's items are its outputs' items one after another; outputs
      // says how many each has. A node that says nothing has one output.
      var lengths = data && isArray(data.outputs) && data.outputs.length > 0 ? data.outputs : null;
      var latest = data && typeof data.runIndex === 'number' ? data.runIndex : 0;
      var outputs = [];
      // read is one output of the node's latest run, the only run kept, as
      // a read of output and run asks for it: the latest run is read when
      // it is named by its number or as n8n's -1, and an earlier one is
      // refused rather than answered with the latest. The expression
      // engine's $items says the same, in the same words.
      function read(label, output, run) {
        executed();
        if (output !== undefined && (typeof output !== 'number' || output < 0 || output % 1 !== 0)) {
          throw new Error(label + ' names output ' + StringType(output) + ' of node "' + name + '", which has no such output');
        }
        if (run !== undefined && !(run === -1 || run === latest)) {
          if (typeof run === 'number' && run >= 0 && run < latest && run % 1 === 0) {
            throw new Error(refusal('reads run ' + run + ' of node "' + name + '", but only its latest run, ' + latest + ', is kept', snapshot.advice));
          }
          throw new Error(label + ' names run ' + StringType(run) + ' of node "' + name + '", which has no such run');
        }
        if (output === undefined) return items;
        if (lengths === null ? output !== 0 : output >= lengths.length) {
          throw new Error(label + ' names output ' + output + ' of node "' + name + '", which has no such output');
        }
        if (lengths === null) return items;
        if (outputs[output] === undefined) {
          var start = 0;
          for (var before = 0; before < output; before++) start += lengths[before];
          outputs[output] = apply(arraySlice, items, [start, start + lengths[output]]);
        }
        return outputs[output];
      }
      readers.set(name, read);
      var view = {
        // With no branch every output is read: which of the node's outputs
        // feeds this one, n8n's default, is not known here.
        all: function (branchIndex, runIndex) {
          return read('$("' + name + '").all()', branchIndex === null ? undefined : branchIndex, runIndex);
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
    function readOutput(name, label, output, run) {
      nodeView(name);
      return readers.get(String(name))(label, output, run);
    }
    // $items is n8n's older spelling of the same reads, still answered in a
    // Code node: with no name, the node's own input (the very list `items`
    // is); with one, one output of that node, output 0 unless the second
    // argument is a number that names another (n8n reads any falsy one as
    // 0), of its latest run unless the third names one. A null name reads
    // the input, as an expression's $items does.
    define('$items', function $items(name, outputIndex, runIndex) {
      if (name === undefined || name === null) return input;
      return readOutput(name, '$items()', outputIndex ? outputIndex : 0, runIndex);
    });
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
      if (!constructing(this, new.target)) return RegExpType(pattern, flags);
      return construct(RegExpType, [pattern, flags], new.target === RegExpGuard ? RegExpType : new.target);
    }
    adopt(RegExpGuard, RegExpType, 'RegExp', []);
    // goja splits and matches on its fast path only when a regular
    // expression's species is its own RegExp. undefined makes the species the
    // intrinsic RegExp without handing it to the code; a subclass is still
    // its own species.
    defineProperty(RegExpGuard, SymbolType.species, {
      get: function () { return this === RegExpGuard ? undefined : this; },
      configurable: true, enumerable: false,
    });
    define('RegExp', RegExpGuard);

    if (eachItem) {
      unavailable('items', 'items is only available when the code runs once for all items; use $input.item or $json');
    }

    boundAllocations(snapshot.caps, snapshot.advice);

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

    // this.helpers is n8n's host API for code. The helpers module fills in
    // the ones that run; any other the code reaches refuses by name rather
    // than reading as missing. then and toJSON stay plain, since await and
    // JSON.stringify probe for them.
    var helpers = {};
    var self = {
      helpers: new ProxyType(helpers, {
        get: function (target, name) {
          if (typeof name !== 'string' || name in target || name === 'then' || name === 'toJSON') return target[name];
          return function () { throw new ErrorType(refusal('uses this.helpers.' + name, snapshot.advice)); };
        },
      }),
    };

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
      hostCall: host.call,
      staticData: host.staticData,
      helpers: helpers,
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
      staticData: shipped.helpers.staticData,
      // run calls the code through its wrapper, whose parameters are the
      // mode's roots in wrapper.go's order. All-items code reads the per-item
      // roots at the first item, as n8n's does.
      run: function (body, itemIndex) {
        current = itemIndex;
        codeBody = body;
        var item = input[itemIndex];
        var json = item === undefined ? undefined : item.json;
        var args = eachItem ?
          [json, binaryOf(item), itemIndex, itemIndex, inputRoot] :
          [input, inputRoot, json, binaryOf(item), itemIndex, itemIndex];
        return apply(body, self, args);
      },
      // sort runs a Sort node's comparator: the wrapper hands it back, and
      // the indexes of a private copy of the input are sorted by what it
      // says of the items at them, so a comparator that changes `items`
      // changes only its own view. The result is that order as text, built
      // here rather than by JSON.stringify, which would honour a toJSON the
      // comparator put on Array.prototype. The items stay where they are.
      //
      // where locates the comparator's one return, or is empty when it has
      // several; a comparator that answered with nothing may have fallen off
      // its end instead, so it is not located.
      sort: function (wrapper, where) {
        codeBody = wrapper;
        var count = input.length;
        var own = [];
        var order = [];
        for (var index = 0; index < count; index++) {
          own[index] = input[index];
          order[index] = index;
        }
        var compare = apply(wrapper, self, [input, inputRoot]);
        apply(arraySort, order, [function (left, right) {
          var answer = apply(compare, self, [own[left], own[right]]);
          if (typeof answer !== 'number' || answer !== answer) {
            throw new InvalidReturn('the comparator returned ' + (answer !== answer ? 'NaN' : kindOf(answer)) +
              ' comparing item ' + left + ' with item ' + right + '; it must return a number: less than 0 when a goes first, ' +
              'more than 0 when b does, and 0 when they tie' + (answer === undefined ? '' : where));
          }
          return answer;
        }]);
        var text = '[';
        for (var position = 0; position < count; position++) {
          text += (position > 0 ? ',' : '') + order[position];
        }
        return text + ']';
      },
    };
  }

  return { normalise: normalise, stringify: stringify, describe: describe, install: install };
})()

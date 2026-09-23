// KilasFlow's own code, written for internal/jsrun from the WHATWG and
// ECMAScript specifications and Node's documented globals. It is not derived
// from Node's or n8n's source.
//
// The globals a Node 24 script can use without importing anything, which
// goja does not have: TextEncoder and TextDecoder, atob and btoa,
// structuredClone, queueMicrotask, DOMException, and ES2024's
// Object.groupBy and Map.groupBy. Each is defined only where goja lacks it.
(function (kit) {
  'use strict';

  var global = kit.global;
  var apply = kit.apply;
  var Buffer = global.Buffer;
  var Uint8ArrayType = Uint8Array;
  var ArrayBufferType = ArrayBuffer;
  var isView = ArrayBuffer.isView;
  var PromiseType = Promise;
  var MapType = Map;
  var SetType = Set;
  var DateType = Date;
  var RegExpType = RegExp;
  var ErrorType = Error;
  var getPrototypeOf = Object.getPrototypeOf;
  var ownKeys = Object.keys;
  var isArray = Array.isArray;
  var iterator = Symbol.iterator;

  function missing(name) {
    return typeof global[name] === 'undefined';
  }

  function DOMException(message, name) {
    var error = new ErrorType(message === undefined ? '' : String(message));
    Object.setPrototypeOf(error, DOMException.prototype);
    Object.defineProperty(error, 'name', { value: name === undefined ? 'Error' : String(name), configurable: true, writable: true });
    return error;
  }
  DOMException.prototype = Object.create(ErrorType.prototype, {
    constructor: { value: DOMException, writable: true, configurable: true },
  });
  if (missing('DOMException')) kit.define('DOMException', DOMException);

  function bytesOf(value) {
    if (value === undefined) return new Uint8ArrayType(0);
    if (value instanceof ArrayBufferType) return new Uint8ArrayType(value);
    if (isView(value)) return new Uint8ArrayType(value.buffer, value.byteOffset, value.byteLength);
    throw new TypeError('The "input" argument must be an instance of ArrayBuffer or ArrayBufferView');
  }

  // ---- TextEncoder and TextDecoder: UTF-8 and UTF-16LE -----------------------

  function TextEncoder() {
    if (!(this instanceof TextEncoder)) throw new TypeError("Class constructor TextEncoder cannot be invoked without 'new'");
  }
  Object.defineProperty(TextEncoder.prototype, 'encoding', { get: function () { return 'utf-8'; } });
  TextEncoder.prototype.encode = function encode(input) {
    return new Uint8ArrayType(kit.native('codec.decode', input === undefined ? '' : String(input), 'utf8'));
  };
  TextEncoder.prototype.encodeInto = function encodeInto(source, destination) {
    var bytes = this.encode(source);
    var written = Math.min(bytes.length, destination.length);
    // Never split a character: back off to the start of the last one that fits.
    while (written < bytes.length && written > 0 && (bytes[written] & 0xc0) === 0x80) written--;
    destination.set(bytes.subarray(0, written));
    return { read: kit.native('codec.encode', bytes.subarray(0, written), 'utf16le').length, written: written };
  };

  var decoderLabels = {
    'utf-8': 'utf-8', 'utf8': 'utf-8', 'unicode-1-1-utf-8': 'utf-8',
    'utf-16le': 'utf-16le', 'utf-16': 'utf-16le',
  };

  function TextDecoder(label, options) {
    if (!(this instanceof TextDecoder)) throw new TypeError("Class constructor TextDecoder cannot be invoked without 'new'");
    var name = decoderLabels[String(label === undefined ? 'utf-8' : label).trim().toLowerCase()];
    if (name === undefined) {
      throw new RangeError('The "' + label + '" encoding is not supported');
    }
    this._encoding = name;
    this._fatal = !!(options && options.fatal);
    this._ignoreBOM = !!(options && options.ignoreBOM);
  }
  Object.defineProperty(TextDecoder.prototype, 'encoding', { get: function () { return this._encoding; } });
  Object.defineProperty(TextDecoder.prototype, 'fatal', { get: function () { return this._fatal; } });
  Object.defineProperty(TextDecoder.prototype, 'ignoreBOM', { get: function () { return this._ignoreBOM; } });
  TextDecoder.prototype.decode = function decode(input) {
    var bytes = bytesOf(input);
    var utf8 = this._encoding === 'utf-8';
    if (!this._ignoreBOM) {
      if (utf8 && bytes[0] === 0xef && bytes[1] === 0xbb && bytes[2] === 0xbf) bytes = bytes.subarray(3);
      if (!utf8 && bytes[0] === 0xff && bytes[1] === 0xfe) bytes = bytes.subarray(2);
    }
    var text = kit.native('codec.encode', bytes, utf8 ? 'utf8' : 'utf16le');
    if (this._fatal && utf8 && text.indexOf('�') >= 0 && !kit.native('codec.validUTF8', bytes)) {
      var error = new TypeError('The encoded data was not valid for encoding utf-8');
      error.code = 'ERR_ENCODING_INVALID_ENCODED_DATA';
      throw error;
    }
    return text;
  };
  if (missing('TextEncoder')) kit.define('TextEncoder', TextEncoder);
  if (missing('TextDecoder')) kit.define('TextDecoder', TextDecoder);

  // ---- atob and btoa ------------------------------------------------------

  if (missing('btoa')) {
    kit.define('btoa', function btoa(data) {
      if (arguments.length === 0) throw new TypeError('The "data" argument must be specified');
      var text = String(data);
      for (var index = 0; index < text.length; index++) {
        if (text.charCodeAt(index) > 0xff) throw new DOMException('Invalid character', 'InvalidCharacterError');
      }
      return kit.native('codec.encode', new Uint8ArrayType(kit.native('codec.decode', text, 'latin1')), 'base64');
    });
  }
  if (missing('atob')) {
    kit.define('atob', function atob(data) {
      if (arguments.length === 0) throw new TypeError('The "data" argument must be specified');
      // WHATWG forgiving-base64: ASCII whitespace is ignored, padding must be
      // right, and anything else outside the alphabet is an error.
      var text = String(data).replace(/[\t\n\f\r ]/g, '');
      if (text.length % 4 === 0) text = text.replace(/==?$/, '');
      if (text.length % 4 === 1 || /[^A-Za-z0-9+/]/.test(text)) {
        throw new DOMException('The string to be decoded is not correctly encoded.', 'InvalidCharacterError');
      }
      return kit.native('codec.encode', new Uint8ArrayType(kit.native('codec.decode', text, 'base64')), 'latin1');
    });
  }

  // ---- structuredClone -------------------------------------------------------

  function cloneError(value) {
    return new DOMException(String(value) + ' could not be cloned.', 'DataCloneError');
  }

  // clone copies what the structured clone algorithm copies: primitives,
  // wrappers, Date, RegExp, Map, Set, ArrayBuffer and views, Error, arrays
  // and plain objects, keeping shared references and cycles. Everything
  // else, such as a function or a symbol, cannot be cloned.
  function clone(value, seen) {
    var type = typeof value;
    if (type === 'symbol' || type === 'function') throw cloneError(type === 'symbol' ? value.toString() : 'function');
    if (value === null || type !== 'object') return value;
    if (seen.has(value)) return seen.get(value);
    var copy;
    if (value instanceof DateType) {
      copy = new DateType(value.getTime());
    } else if (value instanceof RegExpType) {
      copy = new RegExpType(value.source, value.flags);
    } else if (value instanceof ArrayBufferType) {
      copy = value.slice(0);
    } else if (isView(value)) {
      var buffer = clone(value.buffer, seen);
      copy = new value.constructor(buffer, value.byteOffset, value instanceof DataView ? value.byteLength : value.length);
      if (Buffer && value instanceof Buffer) copy = new Uint8ArrayType(buffer, value.byteOffset, value.length);
    } else if (value instanceof MapType) {
      copy = new MapType();
      seen.set(value, copy);
      value.forEach(function (entry, key) { copy.set(clone(key, seen), clone(entry, seen)); });
      return copy;
    } else if (value instanceof SetType) {
      copy = new SetType();
      seen.set(value, copy);
      value.forEach(function (entry) { copy.add(clone(entry, seen)); });
      return copy;
    } else if (value instanceof ErrorType) {
      copy = new ErrorType(value.message);
      copy.name = value.name;
      if (value.stack !== undefined) copy.stack = value.stack;
    } else if (value instanceof Boolean || value instanceof Number || value instanceof String) {
      copy = Object(value.valueOf());
    } else if (value instanceof PromiseType || value instanceof WeakMap || value instanceof WeakSet) {
      throw cloneError('#<' + getPrototypeOf(value).constructor.name + '>');
    } else if (isArray(value)) {
      copy = new Array(value.length);
      seen.set(value, copy);
      ownKeys(value).forEach(function (key) { copy[key] = clone(value[key], seen); });
      return copy;
    } else {
      copy = {};
      seen.set(value, copy);
      ownKeys(value).forEach(function (key) { copy[key] = clone(value[key], seen); });
      return copy;
    }
    seen.set(value, copy);
    return copy;
  }
  if (missing('structuredClone')) {
    kit.define('structuredClone', function structuredClone(value) {
      if (arguments.length === 0) throw new TypeError('The "value" argument must be specified');
      return clone(value, new MapType());
    });
  }

  // ---- queueMicrotask --------------------------------------------------------

  if (missing('queueMicrotask')) {
    kit.define('queueMicrotask', function queueMicrotask(callback) {
      if (typeof callback !== 'function') throw new TypeError('The "callback" argument must be of type function');
      PromiseType.resolve().then(function () { callback(); });
    });
  }

  // ---- Object.groupBy and Map.groupBy (ES2024) -------------------------------

  function each(items, visit) {
    if (items === null || items === undefined) throw new TypeError('groupBy requires an iterable');
    var index = 0;
    var source = items[iterator]();
    for (var step = source.next(); !step.done; step = source.next()) visit(step.value, index++);
  }
  if (typeof Object.groupBy !== 'function') {
    Object.defineProperty(Object, 'groupBy', {
      configurable: true, writable: true,
      value: function groupBy(items, callback) {
        var groups = Object.create(null);
        each(items, function (value, index) {
          var key = callback(value, index);
          key = typeof key === 'symbol' ? key : String(key);
          if (!Object.prototype.hasOwnProperty.call(groups, key)) groups[key] = [];
          groups[key].push(value);
        });
        return groups;
      },
    });
  }
  if (typeof MapType.groupBy !== 'function') {
    Object.defineProperty(MapType, 'groupBy', {
      configurable: true, writable: true,
      value: function groupBy(items, callback) {
        var groups = new MapType();
        each(items, function (value, index) {
          var key = callback(value, index);
          if (key === 0) key = 0; // -0 groups with 0
          if (!groups.has(key)) groups.set(key, []);
          groups.get(key).push(value);
        });
        return groups;
      },
    });
  }

  return undefined;
})

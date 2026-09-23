// KilasFlow's own code, written for internal/jsrun from Node's documented
// Buffer API. It is not derived from Node's or n8n's source.
//
// goja_nodejs provides the Buffer class and its read and write methods. This
// module adds what that Buffer lacks, and what n8n bodies use: every encoding
// Node names (latin1, binary, ascii, ucs2, utf16le and base64url among them,
// matched without regard to case), byteLength, isBuffer, isEncoding,
// compare, toJSON, and a slice that is a view, as Node's is. It also bounds
// every allocation a number decides, as the typed arrays are bounded.
(function (kit) {
  'use strict';

  var Buffer = kit.global.Buffer;
  var proto = Buffer.prototype;
  var apply = kit.apply;
  var Uint8ArrayType = Uint8Array;
  var ArrayBufferType = ArrayBuffer;
  var isView = ArrayBuffer.isView;
  var nativeFrom = Buffer.from;
  var nativeAlloc = Buffer.alloc;
  var nativeConcat = Buffer.concat;
  var subarray = Uint8Array.prototype.subarray;
  var setBytes = Uint8Array.prototype.set;
  var fillBytes = Uint8Array.prototype.fill;
  var indexOfByte = Uint8Array.prototype.indexOf;
  var defineProperty = Object.defineProperty;
  var min = Math.min;
  var max = Math.max;
  var floor = Math.floor;

  var encodings = {
    'utf8': 1, 'utf-8': 1, 'hex': 1, 'base64': 1, 'base64url': 1, 'latin1': 1, 'binary': 1,
    'ascii': 1, 'ucs2': 1, 'ucs-2': 1, 'utf16le': 1, 'utf-16le': 1,
  };

  function known(encoding) {
    return typeof encoding === 'string' && encodings[encoding.toLowerCase()] === 1;
  }

  function checkEncoding(encoding) {
    if (encoding !== undefined && encoding !== null && !known(encoding)) {
      throw new TypeError('Unknown encoding: ' + encoding);
    }
  }

  function bytesAllowed(size) {
    if (size > kit.caps.bytes) throw kit.tooLarge('a buffer of bytes', size, kit.caps.bytes);
  }

  // decoded is a string's bytes in an encoding, as a Buffer.
  function decoded(text, encoding) {
    checkEncoding(encoding);
    return apply(nativeFrom, Buffer, [kit.native('codec.decode', String(text), encoding || 'utf8')]);
  }

  // bytesOf is a byte view of any binary value, for the natives.
  function bytesOf(value) {
    if (value instanceof ArrayBufferType) return new Uint8ArrayType(value);
    return new Uint8ArrayType(value.buffer, value.byteOffset, value.byteLength);
  }

  function method(owner, name, fn) {
    defineProperty(fn, 'name', { value: name });
    defineProperty(owner, name, { value: fn, writable: true, configurable: true, enumerable: false });
  }

  // clampIndex reads a start or end the way Buffer's methods do.
  function clampIndex(value, fallback, length) {
    if (value === undefined) return fallback;
    var index = floor(Number(value)) || 0;
    if (index < 0) index = max(length + index, 0);
    return min(index, length);
  }

  method(Buffer, 'from', function (value, encodingOrOffset, length) {
    if (typeof value === 'string') return decoded(value, encodingOrOffset);
    if (value !== null && typeof value === 'object' && !(value instanceof ArrayBufferType) && !isView(value)) {
      bytesAllowed(kit.lengthOf(value));
    }
    return apply(nativeFrom, Buffer, arguments);
  });

  method(Buffer, 'alloc', function (size, fill, encoding) {
    if (typeof size === 'number') bytesAllowed(size);
    if (typeof fill === 'string' && fill !== '') {
      var buffer = apply(nativeAlloc, Buffer, [size]);
      return apply(proto.fill, buffer, [fill, 0, buffer.length, encoding]);
    }
    return apply(nativeAlloc, Buffer, arguments);
  });
  method(Buffer, 'allocUnsafe', function (size) { return Buffer.alloc(size); });
  method(Buffer, 'allocUnsafeSlow', function (size) { return Buffer.alloc(size); });

  method(Buffer, 'concat', function (list, totalLength) {
    var total = 0;
    for (var index = 0; index < kit.lengthOf(list); index++) total += kit.lengthOf(list[index]);
    if (totalLength !== undefined) total = Number(totalLength);
    bytesAllowed(total);
    return apply(nativeConcat, Buffer, arguments);
  });

  method(Buffer, 'byteLength', function (value, encoding) {
    if (typeof value === 'string') return decoded(value, encoding).length;
    if (value instanceof ArrayBufferType || isView(value)) return value.byteLength;
    throw new TypeError('The "string" argument must be of type string or an instance of Buffer or ArrayBuffer');
  });

  method(Buffer, 'isBuffer', function (value) { return value instanceof Buffer; });
  method(Buffer, 'isEncoding', function (encoding) { return known(encoding); });

  function compareBytes(a, b) {
    var length = min(a.length, b.length);
    for (var index = 0; index < length; index++) {
      if (a[index] !== b[index]) return a[index] < b[index] ? -1 : 1;
    }
    return a.length === b.length ? 0 : a.length < b.length ? -1 : 1;
  }
  method(Buffer, 'compare', function (a, b) { return compareBytes(a, b); });

  method(proto, 'toString', function (encoding, start, end) {
    checkEncoding(encoding);
    var from = clampIndex(start, 0, this.length);
    var to = clampIndex(end, this.length, this.length);
    if (to <= from) return '';
    return kit.native('codec.encode', bytesOf(apply(subarray, this, [from, to])), encoding || 'utf8');
  });

  method(proto, 'toJSON', function () {
    var data = [];
    for (var index = 0; index < this.length; index++) data.push(this[index]);
    return { type: 'Buffer', data: data };
  });

  // Node's slice is a view over the same memory, not a copy.
  method(proto, 'slice', function (start, end) {
    return apply(proto.subarray, this, [clampIndex(start, 0, this.length), clampIndex(end, this.length, this.length)]);
  });

  // write(string[, offset[, length]][, encoding])
  method(proto, 'write', function (string, offset, length, encoding) {
    if (typeof offset === 'string') {
      encoding = offset;
      offset = undefined;
      length = undefined;
    } else if (typeof length === 'string') {
      encoding = length;
      length = undefined;
    }
    var at = offset === undefined ? 0 : offset >>> 0;
    var room = max(this.length - at, 0);
    var data = decoded(string, encoding);
    var count = min(data.length, room, length === undefined ? room : length >>> 0);
    apply(setBytes, this, [apply(subarray, data, [0, count]), at]);
    return count;
  });

  // fill(value[, offset[, end]][, encoding]) repeats a string's bytes.
  method(proto, 'fill', function (value, offset, end, encoding) {
    if (typeof offset === 'string') {
      encoding = offset;
      offset = undefined;
      end = undefined;
    } else if (typeof end === 'string') {
      encoding = end;
      end = undefined;
    }
    var from = clampIndex(offset, 0, this.length);
    var to = clampIndex(end, this.length, this.length);
    if (typeof value !== 'string') {
      var pattern = value;
      if (value instanceof Uint8ArrayType) pattern = value.length ? value[0] : 0;
      apply(fillBytes, this, [pattern, from, to]);
      return this;
    }
    var bytes = decoded(value, encoding);
    if (bytes.length === 0) {
      apply(fillBytes, this, [0, from, to]);
      return this;
    }
    for (var at = from; at < to; at += bytes.length) {
      apply(setBytes, this, [apply(subarray, bytes, [0, min(bytes.length, to - at)]), at]);
    }
    return this;
  });

  method(proto, 'compare', function (target) { return compareBytes(this, target); });

  method(proto, 'copy', function (target, targetStart, sourceStart, sourceEnd) {
    var to = targetStart === undefined ? 0 : targetStart >>> 0;
    var from = clampIndex(sourceStart, 0, this.length);
    var until = clampIndex(sourceEnd, this.length, this.length);
    var count = min(until - from, max(target.length - to, 0));
    if (count <= 0) return 0;
    apply(setBytes, target, [apply(subarray, this, [from, from + count]), to]);
    return count;
  });

  // indexOf finds a byte, a Buffer or a string's bytes, as Node's does; the
  // typed array's own would compare a string to numbers and find nothing.
  function search(haystack, value, start, encoding, last) {
    if (typeof value === 'number') {
      value = value & 0xff;
      if (!last) return apply(indexOfByte, haystack, [value, start]);
    }
    var needle = typeof value === 'number' ? [value]
      : typeof value === 'string' ? decoded(value, encoding)
      : value;
    var length = haystack.length;
    var span = needle.length;
    if (span === 0) return last ? length : min(start, length);
    var first = last ? min(start, length - span) : start;
    for (var at = first; last ? at >= 0 : at <= length - span; at += last ? -1 : 1) {
      var match = true;
      for (var index = 0; index < span; index++) {
        if (haystack[at + index] !== needle[index]) {
          match = false;
          break;
        }
      }
      if (match) return at;
    }
    return -1;
  }
  method(proto, 'indexOf', function (value, byteOffset, encoding) {
    if (typeof byteOffset === 'string') {
      encoding = byteOffset;
      byteOffset = undefined;
    }
    return search(this, value, clampIndex(byteOffset, 0, this.length), encoding, false);
  });
  method(proto, 'lastIndexOf', function (value, byteOffset, encoding) {
    if (typeof byteOffset === 'string') {
      encoding = byteOffset;
      byteOffset = undefined;
    }
    return search(this, value, clampIndex(byteOffset, this.length, this.length), encoding, true);
  });
  method(proto, 'includes', function (value, byteOffset, encoding) {
    return apply(proto.indexOf, this, [value, byteOffset, encoding]) !== -1;
  });

  return { Buffer: Buffer, kMaxLength: kit.caps.bytes, constants: { MAX_LENGTH: kit.caps.bytes } };
})

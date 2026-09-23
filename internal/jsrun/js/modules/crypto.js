// KilasFlow's own code, written for internal/jsrun from Node's documented
// `crypto` API. It is not derived from Node's or n8n's source.
//
// The part of Node's crypto that Code nodes reach for: hashes and HMACs,
// random values, timing-safe comparison, PBKDF2 and scrypt, and AES in CBC,
// CTR and GCM. The cryptography is Go's, through the crypto.* natives
// (crypto.go). This file gives it Node's shape: the argument checks, the
// encodings, update() and final() returning output as they go, and the errors
// Node throws, with their codes.
//
// A hash or cipher in progress keeps its state here, on the object the code
// holds, and hands it to Go on every call; Go keeps nothing between calls.
//
// Every VM runs this factory and most Code nodes never touch crypto, so the
// factory only captures the built-ins and hands out two shells: the global
// crypto, and a module object that builds the real module, with its ninety
// or so functions, the first time the code reaches into it. That keeps the
// factory's cost to that of util's.
//
// Where this differs from Node:
//
//   - a GCM message is at most MaxBytesPerCall bytes, because its tag is
//     computed over the whole message in one call;
//   - PBKDF2 and scrypt refuse a cost one call could not finish promptly
//     (MaxPBKDF2Rounds, MaxScryptCost), and scrypt more memory than one call
//     may use;
//   - an exception thrown by a callback given to randomBytes, randomInt,
//     pbkdf2 or scrypt does not fail the run, as an uncaught one would in
//     Node;
//   - the rest of Node's crypto, and crypto.subtle, refuse by name.
(function (kit) {
  'use strict';

  var native = kit.native;
  var apply = kit.apply;
  var global = kit.global;
  var caps = kit.caps;
  var Bytes = Uint8Array;
  var ArrayBufferType = ArrayBuffer;
  var isView = ArrayBuffer.isView;
  var setBytes = Object.getPrototypeOf(Uint8Array.prototype).set;
  var push = Array.prototype.push;
  var then = Promise.prototype.then;
  var settled = Promise.resolve();
  var ProxyType = Proxy;
  var create = Object.create;
  var defineProperty = Object.defineProperty;
  var ownKeys = Reflect.ownKeys;
  var ownDescriptor = Reflect.getOwnPropertyDescriptor;
  var defineOwn = Reflect.defineProperty;
  var deleteOwn = Reflect.deleteProperty;
  var isInteger = Number.isInteger;
  var isSafeInteger = Number.isSafeInteger;
  var floor = Math.floor;
  var pow = Math.pow;
  var StringType = String;
  var ErrorType = Error;
  var TypeErrorType = TypeError;
  var RangeErrorType = RangeError;
  var hidden = Symbol('kilasflow.crypto');
  var integerArrays = [Int8Array, Uint8Array, Uint8ClampedArray, Int16Array, Uint16Array, Int32Array, Uint32Array];
  if (typeof BigInt64Array === 'function') integerArrays.push(BigInt64Array, BigUint64Array);

  var built;
  function load() {
    if (built === undefined) built = build();
    return built;
  }

  // The shells. The global crypto has Web Crypto's two methods, as Node's has,
  // not enumerable there either.
  var webcrypto = {};
  defineProperty(webcrypto, 'getRandomValues', {
    value: function getRandomValues(data) { return load().getRandomValues(data); },
    writable: true, configurable: true,
  });
  defineProperty(webcrypto, 'randomUUID', {
    value: function randomUUID(options) { return load().randomUUID(options); },
    writable: true, configurable: true,
  });
  // crypto.subtle is Web Crypto's asynchronous API, which this runtime does
  // not have; reading it says so rather than handing back undefined.
  var subtle = {
    get: function () { throw new ErrorType(kit.refusal('uses crypto.subtle')); },
    configurable: true,
    enumerable: false,
  };
  defineProperty(webcrypto, 'subtle', subtle);
  kit.define('crypto', webcrypto);

  function build() {
    // ---- Errors, in Node's words and with its codes ----------------------------

    function coded(error, code) {
      error.code = code;
      return error;
    }

    // number shows a number as Node's range errors do, with separators in a
    // whole number past 2**32.
    function number(value) {
      var text = StringType(value);
      if (typeof value !== 'number' || !isInteger(value) || (value <= 4294967296 && value >= -4294967296) || text.indexOf('e') >= 0) {
        return text;
      }
      var sign = value < 0 ? '-' : '';
      var digits = sign ? text.slice(1) : text;
      var grouped = '';
      while (digits.length > 3) {
        grouped = '_' + digits.slice(-3) + grouped;
        digits = digits.slice(0, -3);
      }
      return sign + digits + grouped;
    }

    function received(value) {
      if (value === null || value === undefined) return ' Received ' + value;
      if (typeof value === 'function') return ' Received function ' + (value.name || '<anonymous>');
      if (typeof value === 'object') {
        var constructor = value.constructor;
        if (constructor && typeof constructor.name === 'string' && constructor.name !== '') {
          return ' Received an instance of ' + constructor.name;
        }
        return ' Received ' + kit.inspect(value, 1);
      }
      var shown = typeof value === 'string'
        ? "'" + (value.length > 28 ? value.slice(0, 25) + '...' : value) + "'"
        : StringType(value);
      return ' Received type ' + typeof value + ' (' + shown + ')';
    }

    function invalidType(name, expected, value) {
      return coded(new TypeErrorType('The "' + name + '" argument must be ' + expected + '.' + received(value)), 'ERR_INVALID_ARG_TYPE');
    }

    function outOfRange(name, range, value) {
      return coded(new RangeErrorType('The value of "' + name + '" is out of range. It must be ' + range + '. Received ' + number(value)), 'ERR_OUT_OF_RANGE');
    }

    function invalidState(operation) {
      return coded(new ErrorType(operation ? 'Invalid state for operation ' + operation : 'Invalid state'), 'ERR_CRYPTO_INVALID_STATE');
    }

    function finalized() {
      return coded(new ErrorType('Digest already called'), 'ERR_CRYPTO_HASH_FINALIZED');
    }

    function invalidDigest(name) {
      return coded(new TypeErrorType('Invalid digest: ' + name), 'ERR_CRYPTO_INVALID_DIGEST');
    }

    function unsupportedDigest() {
      return new ErrorType('Digest method not supported');
    }

    function unauthenticated() {
      return new ErrorType('Unsupported state or unable to authenticate data');
    }

    function wrongFinalBlockLength() {
      return coded(new ErrorType('error:1C80006B:Provider routines::wrong final block length'), 'ERR_OSSL_WRONG_FINAL_BLOCK_LENGTH');
    }

    function badDecrypt() {
      return coded(new ErrorType('error:1C800064:Provider routines::bad decrypt'), 'ERR_OSSL_BAD_DECRYPT');
    }

    function invalidScrypt(detail) {
      return coded(new RangeErrorType('Invalid scrypt params' + (detail ? ': ' + detail : '')), 'ERR_CRYPTO_INVALID_SCRYPT_PARAMS');
    }

    function derivingFailed() {
      return new ErrorType('Deriving bits failed');
    }

    // A Web Crypto error is a DOMException in Node; here it is an Error with
    // the same name and code.
    function domException(name, code, message) {
      var error = new ErrorType(message);
      error.name = name;
      error.code = code;
      return error;
    }

    // integer checks a number argument as Node does: its type, that it is a
    // whole number, and its range.
    function integer(value, name, low, high) {
      if (typeof value !== 'number') throw invalidType(name, 'of type number', value);
      if (!isInteger(value)) throw outOfRange(name, 'an integer', value);
      if (value < low || value > high) throw outOfRange(name, '>= ' + low + ' && <= ' + high, value);
      return value;
    }

    function callbackOf(callback) {
      if (typeof callback !== 'function') throw invalidType('callback', 'of type function', callback);
      return callback;
    }

    // later calls a callback after the current code, as Node's asynchronous
    // forms do.
    function later(callback, args) {
      apply(then, settled, [function () { apply(callback, undefined, args); }]);
    }

    // ---- Bytes in and out ----------------------------------------------------------

    var DATA = 'of type string or an instance of Buffer, TypedArray, or DataView';
    var BYTES = 'of type string or an instance of ArrayBuffer, Buffer, TypedArray, or DataView';
    var KEY = 'of type string or an instance of ArrayBuffer, Buffer, TypedArray, DataView, KeyObject, or CryptoKey';
    var BUFFERS = 'an instance of ArrayBuffer, Buffer, TypedArray, or DataView';
    var empty = new ArrayBufferType(0);

    // encodingOf is Node's name for an encoding, or '' for none it knows.
    function encodingOf(name) {
      if (name === undefined || name === null || name === '') return '';
      return native('crypto.encoding', StringType(name));
    }

    // bytesOf reads data as Node's crypto does: a string in the encoding
    // named, or UTF-8 when it names none Node knows, or the bytes a typed
    // array, a DataView or, where Node takes one, an ArrayBuffer spans. The
    // result views the caller's memory, or fresh memory for a string.
    function bytesOf(value, encoding, name, expected, arrayBuffers) {
      if (typeof value === 'string') return new Bytes(native('codec.decode', value, encodingOf(encoding) || 'utf8'));
      if (isView(value)) return new Bytes(value.buffer, value.byteOffset, value.byteLength);
      if (arrayBuffers && value instanceof ArrayBufferType) return new Bytes(value);
      throw invalidType(name, expected, value);
    }

    // copyOf copies bytes the code could still change.
    function copyOf(bytes) {
      return new Bytes(bytes);
    }

    function view(bytes, start, length) {
      return new Bytes(bytes.buffer, bytes.byteOffset + start, length);
    }

    function join(first, second) {
      if (first.length === 0) return second;
      if (second.length === 0) return first;
      var joined = new Bytes(first.length + second.length);
      apply(setBytes, joined, [first, 0]);
      apply(setBytes, joined, [second, first.length]);
      return joined;
    }

    // toBuffer gives bytes back as Node does: as a Buffer when the runtime
    // has one, and as a Uint8Array otherwise.
    function toBuffer(arrayBuffer) {
      var BufferType = global.Buffer;
      if (typeof BufferType === 'function' && typeof BufferType.from === 'function') return BufferType.from(arrayBuffer);
      return new Bytes(arrayBuffer);
    }

    // A digest in an encoding Node does not know is a Buffer, as in Node.
    function digestOutput(arrayBuffer, encoding) {
      var name = encodingOf(encoding);
      return name === '' ? toBuffer(arrayBuffer) : native('codec.encode', arrayBuffer, name);
    }

    function withState(prototype, state) {
      var object = create(prototype);
      defineProperty(object, hidden, { value: state });
      return object;
    }

    function stateOf(self, kind) {
      var state = self !== null && typeof self === 'object' ? self[hidden] : undefined;
      if (state === undefined || (kind !== undefined && state.kind !== kind)) throw new TypeErrorType('Illegal invocation');
      return state;
    }

    function illegalConstructor() {
      throw new TypeErrorType('Illegal constructor');
    }

    // ---- Hashes and HMACs ------------------------------------------------------------

    function Hash() { illegalConstructor(); }
    var HashPrototype = Hash.prototype;

    function startHash(algorithm) {
      if (typeof algorithm !== 'string') throw invalidType('algorithm', 'of type string', algorithm);
      var value = native('crypto.hashInit', algorithm);
      if (value === undefined) throw unsupportedDigest();
      return value;
    }

    function createHash(algorithm) {
      var value = startHash(algorithm);
      return withState(HashPrototype, { kind: 'Hash', algorithm: algorithm, value: value, done: false });
    }

    HashPrototype.update = function update(data, inputEncoding) {
      var state = stateOf(this, 'Hash');
      if (state.done) throw finalized();
      state.value = native('crypto.hashUpdate', state.algorithm, state.value, bytesOf(data, inputEncoding, 'data', DATA, false));
      return this;
    };

    HashPrototype.digest = function digest(outputEncoding) {
      var state = stateOf(this, 'Hash');
      if (state.done) throw finalized();
      state.done = true;
      return digestOutput(native('crypto.hashDigest', state.algorithm, state.value), outputEncoding);
    };

    HashPrototype.copy = function copy() {
      var state = stateOf(this, 'Hash');
      if (state.done) throw finalized();
      return withState(HashPrototype, { kind: 'Hash', algorithm: state.algorithm, value: state.value, done: false });
    };

    // hash is Node's one-shot form, hex unless told otherwise.
    function hash(algorithm, data, outputEncoding) {
      var value = startHash(algorithm);
      var bytes = bytesOf(data, undefined, 'data', DATA, false);
      var digest = native('crypto.hashDigest', algorithm, native('crypto.hashUpdate', algorithm, value, bytes));
      return digestOutput(digest, outputEncoding === undefined ? 'hex' : outputEncoding);
    }

    function Hmac() { illegalConstructor(); }
    var HmacPrototype = Hmac.prototype;

    function createHmac(algorithm, key, options) {
      if (typeof algorithm !== 'string') throw invalidType('hmac', 'of type string', algorithm);
      var encoding = options !== null && typeof options === 'object' ? options.encoding : undefined;
      var value = native('crypto.hmacInit', algorithm, bytesOf(key, encoding, 'key', KEY, true));
      if (value === undefined) throw invalidDigest(algorithm);
      return withState(HmacPrototype, { kind: 'Hmac', algorithm: algorithm, value: value, done: false });
    }

    HmacPrototype.update = function update(data, inputEncoding) {
      var state = stateOf(this, 'Hmac');
      if (state.done) throw finalized();
      state.value = native('crypto.hmacUpdate', state.algorithm, state.value, bytesOf(data, inputEncoding, 'data', DATA, false));
      return this;
    };

    // A second digest() is empty rather than an error, as in Node.
    HmacPrototype.digest = function digest(outputEncoding) {
      var state = stateOf(this, 'Hmac');
      var mac = state.done ? empty : native('crypto.hmacDigest', state.algorithm, state.value);
      state.done = true;
      return digestOutput(mac, outputEncoding);
    };

    function getHashes() {
      return native('crypto.names').hashes;
    }

    // ---- Random values ---------------------------------------------------------------

    function randomBytes(size, callback) {
      if (callback !== undefined) callbackOf(callback);
      if (typeof size !== 'number') throw invalidType('size', 'of type number', size);
      if (!(size >= 0 && size <= 2147483647)) throw outOfRange('size', '>= 0 && <= 2147483647', size);
      size = floor(size);
      if (size > caps.bytes) throw kit.tooLarge('a buffer of bytes', size, caps.bytes);
      var bytes = toBuffer(native('crypto.randomBytes', size));
      if (callback === undefined) return bytes;
      later(callback, [null, bytes]);
    }

    function randomUUID(options) {
      if (options !== undefined && (options === null || typeof options !== 'object')) {
        throw invalidType('options', 'of type object', options);
      }
      return native('crypto.randomUUID');
    }

    // randomInt([min, ]max[, callback]) is a whole number in [min, max).
    function randomInt(min, max, callback) {
      var minGiven = max !== undefined && typeof max !== 'function';
      if (!minGiven) {
        callback = max;
        max = min;
        min = 0;
      }
      if (callback !== undefined) callbackOf(callback);
      if (!isSafeInteger(min)) throw invalidType('min', 'a safe integer', min);
      if (!isSafeInteger(max)) throw invalidType('max', 'a safe integer', max);
      if (max <= min) throw outOfRange('max', 'greater than the value of "min" (' + min + ')', max);
      if (max - min > 281474976710655) throw outOfRange(minGiven ? 'max - min' : 'max', '<= 281474976710655', max - min);
      var value = native('crypto.randomInt', min, max);
      if (callback === undefined) return value;
      // Node passes randomInt's callback an undefined error, where the other
      // callbacks here get null.
      later(callback, [undefined, value]);
    }

    // timingSafeEqual's type error, unlike the others, does not say what it
    // was given.
    function bufferOf(value, name) {
      if (isView(value)) return new Bytes(value.buffer, value.byteOffset, value.byteLength);
      if (value instanceof ArrayBufferType) return new Bytes(value);
      throw coded(new TypeErrorType('The "' + name + '" argument must be ' + BUFFERS + '.'), 'ERR_INVALID_ARG_TYPE');
    }

    function timingSafeEqual(buf1, buf2) {
      var left = bufferOf(buf1, 'buf1');
      var right = bufferOf(buf2, 'buf2');
      if (left.length !== right.length) {
        throw coded(new RangeErrorType('Input buffers must have the same byte length'), 'ERR_CRYPTO_TIMING_SAFE_EQUAL_LENGTH');
      }
      return native('crypto.timingSafeEqual', left, right);
    }

    // getRandomValues is Web Crypto's: it fills an integer typed array in
    // place, at most 65,536 bytes at a time.
    function getRandomValues(data) {
      var integers = false;
      for (var index = 0; index < integerArrays.length && !integers; index++) {
        integers = data instanceof integerArrays[index];
      }
      if (!integers) throw domException('TypeMismatchError', 17, 'The data argument must be an integer-type TypedArray');
      if (data.byteLength > 65536) throw domException('QuotaExceededError', 22, 'The requested length exceeds 65,536 bytes');
      var target = new Bytes(data.buffer, data.byteOffset, data.byteLength);
      apply(setBytes, target, [new Bytes(native('crypto.randomBytes', target.length))]);
      return data;
    }

    // ---- Key derivation ----------------------------------------------------------------

    // derivePBKDF2 checks the arguments, which both forms throw for, and
    // derives the key. A key of no bytes is undefined: Node fails to derive
    // one, and reports that the way each form reports a failure.
    function derivePBKDF2(password, salt, iterations, keylen, digest) {
      var passwordBytes = bytesOf(password, undefined, 'password', BYTES, true);
      var saltBytes = bytesOf(salt, undefined, 'salt', BYTES, true);
      integer(iterations, 'iterations', 1, 2147483647);
      integer(keylen, 'keylen', 0, 2147483647);
      if (typeof digest !== 'string') throw invalidType('digest', 'of type string', digest);
      var key = native('crypto.pbkdf2', passwordBytes, saltBytes, iterations, keylen, digest);
      if (key === undefined) throw invalidDigest(digest);
      return keylen === 0 ? undefined : toBuffer(key);
    }

    function pbkdf2Sync(password, salt, iterations, keylen, digest) {
      var key = derivePBKDF2(password, salt, iterations, keylen, digest);
      if (key === undefined) throw derivingFailed();
      return key;
    }

    function pbkdf2(password, salt, iterations, keylen, digest, callback) {
      if (typeof digest === 'function') {
        callback = digest;
        digest = undefined;
      }
      callbackOf(callback);
      var key = derivePBKDF2(password, salt, iterations, keylen, digest);
      later(callback, key === undefined ? [derivingFailed(), undefined] : [null, key]);
    }

    // scryptOption reads one of scrypt's options, which Node takes under
    // either of two names; 0 means the default.
    function scryptOption(options, name, alias, fallback, max) {
      var value = options[name];
      if (alias !== undefined) {
        var other = options[alias];
        if (value !== undefined && other !== undefined) {
          throw coded(new TypeErrorType('Option "' + name + '" cannot be used in combination with option "' + alias + '"'), 'ERR_INCOMPATIBLE_OPTION_PAIR');
        }
        if (value === undefined) {
          value = other;
          name = alias;
        }
      }
      if (value === undefined) return fallback;
      integer(value, name, 0, max);
      return value === 0 ? fallback : value;
    }

    // scryptParameters applies Node's rules: N a power of two, and the limits
    // of RFC 7914 and of maxmem, which Node reports alike, as exceeding
    // memory.
    function scryptParameters(options) {
      var N = 16384;
      var r = 8;
      var p = 1;
      var maxmem = 33554432;
      if (options !== null && typeof options === 'object') {
        N = scryptOption(options, 'N', 'cost', N, 4294967295);
        r = scryptOption(options, 'r', 'blockSize', r, 4294967295);
        p = scryptOption(options, 'p', 'parallelization', p, 4294967295);
        maxmem = scryptOption(options, 'maxmem', undefined, maxmem, 9007199254740991);
      }
      if (N < 2 || (N & (N - 1)) !== 0) throw invalidScrypt('');
      if (128 * r * (N + p + 2) > maxmem || r * p >= 1073741824 || N >= pow(2, 16 * r)) {
        throw invalidScrypt('error:030000AC:digital envelope routines::memory limit exceeded');
      }
      return [N, r, p];
    }

    function scryptSync(password, salt, keylen, options) {
      var passwordBytes = bytesOf(password, undefined, 'password', BYTES, true);
      var saltBytes = bytesOf(salt, undefined, 'salt', BYTES, true);
      integer(keylen, 'keylen', 0, 2147483647);
      var parameters = scryptParameters(options);
      return toBuffer(native('crypto.scrypt', passwordBytes, saltBytes, keylen, parameters[0], parameters[1], parameters[2]));
    }

    function scrypt(password, salt, keylen, options, callback) {
      if (typeof options === 'function') {
        callback = options;
        options = undefined;
      }
      callbackOf(callback);
      later(callback, [null, scryptSync(password, salt, keylen, options)]);
    }

    // ---- AES ciphers -----------------------------------------------------------------

    function Cipheriv() { illegalConstructor(); }
    function Decipheriv() { illegalConstructor(); }
    var CipherPrototype = Cipheriv.prototype;
    var DecipherPrototype = Decipheriv.prototype;

    function checkTagLength(length, required) {
      var valid = required !== undefined ? length === required : length === 4 || length === 8 || (length >= 12 && length <= 16);
      if (!valid) throw coded(new TypeErrorType('Invalid authentication tag length: ' + length), 'ERR_CRYPTO_INVALID_AUTH_TAG');
    }

    function createCipher(prototype, kind, cipher, key, iv, options) {
      if (typeof cipher !== 'string') throw invalidType('cipher', 'of type string', cipher);
      var keyBytes = bytesOf(key, undefined, 'key', KEY, true);
      var ivBytes = iv === null ? new Bytes(0) : bytesOf(iv, undefined, 'iv', BYTES, true);
      var info = native('crypto.cipherInfo', cipher);
      if (info === undefined) throw coded(new ErrorType('Unknown cipher'), 'ERR_CRYPTO_UNKNOWN_CIPHER');
      if (keyBytes.length !== info.keyLength) throw coded(new RangeErrorType('Invalid key length'), 'ERR_CRYPTO_INVALID_KEYLEN');
      var gcm = info.mode === 'gcm';
      if (gcm ? ivBytes.length < 1 || ivBytes.length > 128 : ivBytes.length !== 16) {
        throw coded(new TypeErrorType('Invalid initialization vector'), 'ERR_CRYPTO_INVALID_IV');
      }
      var tagLength = 16;
      var tagFixed = false;
      if (gcm && options !== null && typeof options === 'object' && options.authTagLength !== undefined) {
        tagLength = options.authTagLength;
        if (typeof tagLength !== 'number') {
          throw coded(new TypeErrorType("The property 'options.authTagLength' is invalid. Received " + kit.inspect(tagLength, 1)), 'ERR_INVALID_ARG_VALUE');
        }
        checkTagLength(tagLength);
        tagFixed = true;
      }
      return withState(prototype, {
        kind: kind,
        mode: info.mode,
        decrypt: kind === 'Decipheriv',
        key: copyOf(keyBytes),
        iv: copyOf(ivBytes),
        padding: true,
        // CBC's partial block, held until more data or final().
        pending: new Bytes(0),
        // CTR's and GCM's position in the stream, and GCM's message so far,
        // which its tag is computed over at final().
        offset: 0,
        chunks: [],
        aad: new Bytes(0),
        tag: undefined,
        tagLength: tagLength,
        tagFixed: tagFixed,
        started: false,
        done: false,
        // The string encoding of the output so far, and the bytes held back
        // because they end part way through a character.
        encoding: undefined,
        carry: new Bytes(0),
      });
    }

    function createCipheriv(cipher, key, iv, options) {
      return createCipher(CipherPrototype, 'Cipheriv', cipher, key, iv, options);
    }

    function createDecipheriv(cipher, key, iv, options) {
      return createCipher(DecipherPrototype, 'Decipheriv', cipher, key, iv, options);
    }

    function cipherState(self, kind) {
      var state = stateOf(self, kind);
      if (state.kind !== 'Cipheriv' && state.kind !== 'Decipheriv') throw new TypeErrorType('Illegal invocation');
      return state;
    }

    // CBC returns whole blocks as they fill. Decryption also holds back the
    // last whole block while padding is on, since final() must strip it.
    function cbcUpdate(state, bytes) {
      var all = join(state.pending, bytes);
      var keep = all.length % 16;
      if (state.decrypt && state.padding && keep === 0 && all.length > 0) keep = 16;
      var cut = all.length - keep;
      state.pending = copyOf(view(all, cut, keep));
      if (cut === 0) return empty;
      var blocks = view(all, 0, cut);
      var out = native('crypto.cbc', state.key, state.iv, blocks, state.decrypt, false);
      state.iv = copyOf(state.decrypt ? view(blocks, cut - 16, 16) : new Bytes(out, cut - 16, 16));
      return out;
    }

    function cbcFinal(state) {
      var pending = state.pending;
      if (state.padding) {
        if (!state.decrypt) return native('crypto.cbc', state.key, state.iv, pending, false, true);
        if (pending.length !== 16) throw wrongFinalBlockLength();
        var plain = native('crypto.cbc', state.key, state.iv, pending, true, true);
        if (plain === undefined) throw badDecrypt();
        return plain;
      }
      if (pending.length % 16 !== 0) throw wrongFinalBlockLength();
      return pending.length === 0 ? empty : native('crypto.cbc', state.key, state.iv, pending, state.decrypt, false);
    }

    function advance(state, bytes) {
      var out;
      switch (state.mode) {
        case 'cbc':
          return cbcUpdate(state, bytes);
        case 'ctr':
          out = native('crypto.ctr', state.key, state.iv, state.offset, bytes);
          state.offset += bytes.length;
          return out;
        default:
          var total = state.offset + bytes.length;
          if (total > caps.bytes) throw kit.tooLarge('an AES-GCM message', total, caps.bytes);
          apply(push, state.chunks, [copyOf(bytes)]);
          out = native('crypto.gcm', state.key, state.iv, state.offset, bytes);
          state.offset = total;
          return out;
      }
    }

    function gcmFinal(state) {
      var message = new Bytes(state.offset);
      var at = 0;
      for (var index = 0; index < state.chunks.length; index++) {
        apply(setBytes, message, [state.chunks[index], at]);
        at += state.chunks[index].length;
      }
      state.chunks = [];
      if (!state.decrypt) {
        state.tag = new Bytes(native('crypto.gcmTag', state.key, state.iv, state.aad, message, state.tagLength));
      } else if (state.tag === undefined || !native('crypto.gcmCheck', state.key, state.iv, state.aad, message, state.tag)) {
        throw unauthenticated();
      }
      return empty;
    }

    // outputOf is the encoding output is wanted in, or '' for a Buffer.
    function outputOf(name) {
      if (name === undefined || name === null || name === '' || name === 'buffer') return '';
      var normal = native('crypto.encoding', StringType(name));
      if (normal === '') throw coded(new TypeErrorType('Unknown encoding: ' + name), 'ERR_UNKNOWN_ENCODING');
      return normal;
    }

    // incomplete counts the bytes at the end of the output that cannot be
    // encoded yet: a partial UTF-8 character, half a UTF-16 unit, or the
    // bytes past the last whole three for base64. Node's cipher output
    // carries them into the next call the same way, so the pieces joined
    // equal the whole encoded at once. A UTF-16 high surrogate waits for its
    // pair too: Node hands it back alone, but a string that passes through Go
    // cannot hold one, so here the pair arrives together. Joined, the pieces
    // are the same.
    function incomplete(bytes, encoding) {
      var length = bytes.length;
      switch (encoding) {
        case 'base64':
        case 'base64url':
          return length % 3;
        case 'utf16le':
          var odd = length % 2;
          if (length - odd >= 2) {
            var unit = bytes[length - odd - 2] | (bytes[length - odd - 1] << 8);
            if (unit >= 0xd800 && unit <= 0xdbff) return odd + 2;
          }
          return odd;
        case 'utf8':
          for (var back = 1; back <= 3 && back <= length; back++) {
            var lead = bytes[length - back];
            if ((lead & 0xc0) === 0x80) continue;
            var size = (lead & 0xe0) === 0xc0 ? 2 : (lead & 0xf0) === 0xe0 ? 3 : (lead & 0xf8) === 0xf0 ? 4 : 1;
            return size > back ? back : 0;
          }
          return 0;
      }
      return 0;
    }

    function output(state, arrayBuffer, encoding, last) {
      if (encoding === '') return toBuffer(arrayBuffer);
      if (state.encoding !== undefined && state.encoding !== encoding) throw new ErrorType('Cannot change encoding');
      state.encoding = encoding;
      var all = join(state.carry, new Bytes(arrayBuffer));
      var keep = last ? 0 : incomplete(all, encoding);
      state.carry = copyOf(view(all, all.length - keep, keep));
      return native('codec.encode', view(all, 0, all.length - keep), encoding);
    }

    CipherPrototype.update = DecipherPrototype.update = function update(data, inputEncoding, outputEncoding) {
      var state = cipherState(this);
      if (state.done) throw new ErrorType('Trying to add data in unsupported state');
      var encoding = outputOf(outputEncoding);
      var bytes = bytesOf(data, inputEncoding, 'data', DATA, false);
      state.started = true;
      return output(state, advance(state, bytes), encoding, false);
    };

    CipherPrototype.final = DecipherPrototype.final = function final(outputEncoding) {
      var state = cipherState(this);
      if (state.done) throw invalidState('');
      var encoding = outputOf(outputEncoding);
      state.done = true;
      var out = state.mode === 'cbc' ? cbcFinal(state) : state.mode === 'gcm' ? gcmFinal(state) : empty;
      return output(state, out, encoding, true);
    };

    // Node turns the argument into a boolean, so a call without one turns
    // padding off.
    CipherPrototype.setAutoPadding = DecipherPrototype.setAutoPadding = function setAutoPadding(autoPadding) {
      var state = cipherState(this);
      if (state.done) throw invalidState('setAutoPadding');
      state.padding = !!autoPadding;
      return this;
    };

    // Additional authenticated data goes in before any message, and several
    // calls add up.
    CipherPrototype.setAAD = DecipherPrototype.setAAD = function setAAD(buffer, options) {
      var state = cipherState(this);
      var encoding = options !== null && typeof options === 'object' ? options.encoding : undefined;
      var bytes = bytesOf(buffer, encoding, 'aadbuf', BYTES, true);
      if (state.mode !== 'gcm' || state.started || state.done) throw invalidState('setAAD');
      state.aad = copyOf(join(state.aad, bytes));
      return this;
    };

    CipherPrototype.getAuthTag = function getAuthTag() {
      var state = cipherState(this, 'Cipheriv');
      if (state.mode !== 'gcm' || state.tag === undefined) throw invalidState('getAuthTag');
      return toBuffer(copyOf(state.tag).buffer);
    };

    DecipherPrototype.setAuthTag = function setAuthTag(buffer, encoding) {
      var state = cipherState(this, 'Decipheriv');
      var tag = bytesOf(buffer, encoding, 'buffer', BYTES, true);
      if (state.mode !== 'gcm' || state.done || state.tag !== undefined) throw invalidState('setAuthTag');
      checkTagLength(tag.length, state.tagFixed ? state.tagLength : undefined);
      state.tag = copyOf(tag);
      return this;
    };

    function getCiphers() {
      return native('crypto.names').ciphers;
    }

    // ---- The module ------------------------------------------------------------------

    var api = {
      createHash: createHash,
      createHmac: createHmac,
      hash: hash,
      getHashes: getHashes,
      randomBytes: randomBytes,
      randomUUID: randomUUID,
      randomInt: randomInt,
      timingSafeEqual: timingSafeEqual,
      getRandomValues: getRandomValues,
      pbkdf2: pbkdf2,
      pbkdf2Sync: pbkdf2Sync,
      scrypt: scrypt,
      scryptSync: scryptSync,
      createCipheriv: createCipheriv,
      createDecipheriv: createDecipheriv,
      getCiphers: getCiphers,
      webcrypto: webcrypto,
    };
    defineProperty(api, 'subtle', subtle);

    // The rest of Node's crypto refuses by name, so the code learns which
    // call this server does not run instead of finding it undefined.
    var unsupported = ['argon2', 'argon2Sync', 'checkPrime', 'checkPrimeSync', 'createDiffieHellman',
      'createDiffieHellmanGroup', 'createECDH', 'createPrivateKey', 'createPublicKey', 'createSecretKey', 'createSign',
      'createVerify', 'decapsulate', 'diffieHellman', 'encapsulate', 'generateKey', 'generateKeyPair',
      'generateKeyPairSync', 'generateKeySync', 'generatePrime', 'generatePrimeSync', 'getCipherInfo', 'getCurves',
      'getDiffieHellman', 'hkdf', 'hkdfSync', 'privateDecrypt', 'privateEncrypt', 'publicDecrypt', 'publicEncrypt',
      'randomFill', 'randomFillSync', 'randomUUIDv7', 'sign', 'verify'];
    function refuse(name) {
      var refusal = function () { throw new ErrorType(kit.refusal('uses crypto.' + name)); };
      defineProperty(refusal, 'name', { value: name });
      return refusal;
    }
    for (var index = 0; index < unsupported.length; index++) {
      api[unsupported[index]] = refuse(unsupported[index]);
    }
    return api;
  }

  // The module object stands in for the module until the code first reaches
  // into it, then answers for it: every read, write and listing goes to the
  // module built on that first touch.
  return new ProxyType({}, {
    get: function (_, name) { return load()[name]; },
    set: function (_, name, value) { load()[name] = value; return true; },
    has: function (_, name) { return name in load(); },
    ownKeys: function () { return ownKeys(load()); },
    getOwnPropertyDescriptor: function (_, name) { return ownDescriptor(load(), name); },
    defineProperty: function (_, name, descriptor) { return defineOwn(load(), name, descriptor); },
    deleteProperty: function (_, name) { return deleteOwn(load(), name); },
  });
})

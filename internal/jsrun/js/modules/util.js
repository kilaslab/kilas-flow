// KilasFlow's own code, written for internal/jsrun from Node's documented
// `util` behaviour. It is not derived from Node's or n8n's source.
//
// The part of Node's util that Code nodes reach for: inspect and format, as
// the console renders them, promisify, and the type checks.
(function (kit) {
  'use strict';

  var slice = Array.prototype.slice;
  var PromiseType = Promise;
  var ArrayBufferType = ArrayBuffer;
  var DataViewType = DataView;
  var isView = ArrayBuffer.isView;

  function promisify(original) {
    if (typeof original !== 'function') {
      throw new TypeError('util.promisify expects a function');
    }
    return function () {
      var self = this;
      var args = kit.apply(slice, arguments, []);
      return new PromiseType(function (resolve, reject) {
        args.push(function (error, value) {
          if (error) reject(error);
          else resolve(value);
        });
        kit.apply(original, self, args);
      });
    };
  }

  function is(type) {
    return function (value) { return value instanceof type; };
  }

  var types = {
    isDate: is(Date),
    isRegExp: is(RegExp),
    isPromise: is(PromiseType),
    isMap: is(Map),
    isSet: is(Set),
    isWeakMap: is(WeakMap),
    isWeakSet: is(WeakSet),
    isArrayBuffer: is(ArrayBufferType),
    isDataView: is(DataViewType),
    isNativeError: is(Error),
    isTypedArray: function (value) { return isView(value) && !(value instanceof DataViewType); },
    isUint8Array: is(Uint8Array),
  };

  return {
    // Node's default depth is 2 levels below the value itself.
    inspect: function (value, options) {
      var depth = options && typeof options.depth === 'number' ? options.depth : 2;
      return kit.inspect(value, depth + 1);
    },
    format: function () { return kit.format(arguments); },
    promisify: promisify,
    types: types,
    isArray: Array.isArray,
  };
})

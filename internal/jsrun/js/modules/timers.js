// KilasFlow's own code, written for internal/jsrun from Node's documented
// timers API. It is not derived from Node's or n8n's source.
//
// setTimeout, setInterval and setImmediate, run on the execution's own loop.
// A timer the code is waiting for keeps the code running; one it is not
// waiting for is dropped when the code finishes, so no timer outlives its
// execution. As in Node, a callback must be a function; code in a string is
// refused.
(function (kit) {
  'use strict';

  var apply = kit.apply;
  var slice = Array.prototype.slice;
  var callbacks = new Map();
  var nextId = 1;

  // Timeout is what the set functions return, as in Node. It converts to its
  // id, so code that treats it as a number still works.
  function Timeout(id) {
    Object.defineProperty(this, '_timerId', { value: id });
  }
  Timeout.prototype.ref = function ref() { return this; };
  Timeout.prototype.unref = function unref() { return this; };
  Timeout.prototype.hasRef = function hasRef() { return true; };
  Timeout.prototype.refresh = function refresh() {
    var entry = callbacks.get(this._timerId);
    if (entry) {
      kit.timers.cancel(this._timerId);
      kit.timers.start(this._timerId, entry.delay, fire);
    }
    return this;
  };
  Timeout.prototype.close = function close() { clear(this); return this; };
  Timeout.prototype[Symbol.toPrimitive] = function () { return this._timerId; };

  function fire(id) {
    var entry = callbacks.get(id);
    if (!entry) return;
    if (entry.repeat) kit.timers.start(id, entry.delay, fire);
    else callbacks.delete(id);
    apply(entry.callback, undefined, entry.args);
  }

  function schedule(callback, delay, args, repeat) {
    if (typeof callback !== 'function') {
      throw new TypeError('The "callback" argument must be of type function. Received ' + typeof callback);
    }
    var milliseconds = Number(delay);
    if (!(milliseconds >= 1 && milliseconds <= 2147483647)) milliseconds = 1;
    var id = nextId++;
    callbacks.set(id, { callback: callback, args: args, repeat: repeat, delay: milliseconds });
    kit.timers.start(id, milliseconds, fire);
    return new Timeout(id);
  }

  function clear(handle) {
    var id = handle instanceof Timeout ? handle._timerId : Number(handle);
    if (callbacks.delete(id)) kit.timers.cancel(id);
  }

  kit.define('setTimeout', function setTimeout(callback, delay) {
    return schedule(callback, delay, apply(slice, arguments, [2]), false);
  });
  kit.define('setInterval', function setInterval(callback, delay) {
    return schedule(callback, delay, apply(slice, arguments, [2]), true);
  });
  kit.define('setImmediate', function setImmediate(callback) {
    return schedule(callback, 0, apply(slice, arguments, [1]), false);
  });
  kit.define('clearTimeout', function clearTimeout(handle) { clear(handle); });
  kit.define('clearInterval', function clearInterval(handle) { clear(handle); });
  kit.define('clearImmediate', function clearImmediate(handle) { clear(handle); });

  return undefined;
})

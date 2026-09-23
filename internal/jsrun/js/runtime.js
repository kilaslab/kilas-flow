// KilasFlow's own code, written for internal/jsrun. It is not derived from n8n.
//
// The helpers the runner calls after the user's code returns. They run in the
// same VM, but they are captured before any user code runs and are reachable
// only from Go, so a script cannot replace them. It can still change the
// built-ins they use; that is charged to its time limit and only ever changes
// its own result.
(function () {
  'use strict';

  var isArray = Array.isArray;
  var stringifyJSON = JSON.stringify;
  var ErrorType = Error;

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

  // toItem accepts an item, or a plain object that becomes the item's json.
  function toItem(entry, where) {
    if (!isObject(entry)) {
      throw new InvalidReturn(where + ' is ' + kindOf(entry) + ', but every returned item must be an object');
    }
    if (!('json' in entry)) {
      return { json: entry };
    }
    if (!isObject(entry.json)) {
      throw new InvalidReturn(where + ' has a json that is ' + kindOf(entry.json) + ', but json must be an object');
    }
    var item = { json: entry.json };
    if (entry.binary !== undefined) item.binary = entry.binary;
    if (entry.pairedItem !== undefined) item.pairedItem = entry.pairedItem;
    return item;
  }

  function normalise(result, eachItem) {
    if (eachItem) {
      if (result === undefined || result === null) {
        throw new InvalidReturn('the code returned ' + kindOf(result) + '; when it runs once for each item it must return one object');
      }
      if (isArray(result)) {
        throw new InvalidReturn('the code returned a list; when it runs once for each item it must return one object');
      }
      return [toItem(result, 'the returned value')];
    }
    if (isArray(result)) {
      var items = [];
      for (var index = 0; index < result.length; index++) {
        items.push(toItem(result[index], 'item ' + index));
      }
      return items;
    }
    if (isObject(result)) {
      return [toItem(result, 'the returned value')];
    }
    throw new InvalidReturn('the code returned ' + kindOf(result) + '; it must return a list of items, such as [{ json: { … } }]');
  }

  // stringify is JSON.stringify with a running size estimate, so an oversized
  // result stops early instead of being built whole before Go measures it.
  // The estimate is deliberately loose; Go checks the exact size afterwards.
  function stringify(value, max) {
    var size = 0;
    return stringifyJSON(value, function (key, current) {
      size += key.length + 4;
      switch (typeof current) {
        case 'string': size += current.length + 2; break;
        case 'number': size += 24; break;
        default: size += 5;
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

  return { normalise: normalise, stringify: stringify, describe: describe };
})()

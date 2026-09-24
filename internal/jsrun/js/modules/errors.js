// KilasFlow's own code, written for internal/jsrun from the messages Node 24
// prints, recorded black-box into testdata/parity/errors.json. It is not
// derived from V8's, Node's or n8n's source.
//
// The errors the engine's built-ins throw, in the words V8 uses for them. A
// user moving a workflow from n8n reads the same words for the same mistake,
// and code that tests error.message takes the same branch.
//
// JSON.parse is replaced by one that re-raises goja's SyntaxError (whose
// words come from Go's encoding/json) with V8's message for the same text.
// The other errors are goja's own TypeErrors, which no hook sees as they are
// made: reword rewrites one when the code can first read it, at the start of
// each of the code's catch clauses (the compiled body calls it there) and on
// the way out of an uncaught error.
(function (kit) {
  'use strict';

  var apply = kit.apply;
  var parseJSON = JSON.parse;
  var SyntaxErrorType = SyntaxError;
  var TypeErrorType = TypeError;
  var defineProperty = Object.defineProperty;
  var getOwnPropertyDescriptor = Object.getOwnPropertyDescriptor;
  var hasOwnProperty = Object.prototype.hasOwnProperty;
  var charCodeAt = String.prototype.charCodeAt;
  var charAt = String.prototype.charAt;
  var stringSlice = String.prototype.slice;
  var stringIndexOf = String.prototype.indexOf;
  var regExpTest = RegExp.prototype.test;
  var stringReplace = String.prototype.replace;

  // ---- JSON.parse ---------------------------------------------------------------

  // Whole texts V8 names on their own, without the token.
  var named = { 'undefined': true, 'NaN': true, 'Infinity': true, '[object Object]': true };

  // jsonFault reads source as JSON's grammar does and returns V8's message
  // for its first fault, or undefined when it is valid JSON. It keeps its
  // own stack of open brackets, so a deeply nested text cannot overflow the
  // call stack while it is read.
  function jsonFault(source) {
    var length = source.length;
    var at = 0;
    var open = [];
    var depth = 0;

    function code(position) { return apply(charCodeAt, source, [position]); }
    function isDigit(c) { return c >= 48 && c <= 57; }
    function skip() {
      for (var c = code(at); c === 32 || c === 9 || c === 10 || c === 13; c = code(at)) at++;
    }
    function positioned(text, position) {
      var line = 1;
      var lineStart = 0;
      for (var index = 0; index < position; index++) {
        var c = code(index);
        if (c === 13 && code(index + 1) === 10) index++;
        if (c === 10 || c === 13) {
          line++;
          lineStart = index + 1;
        }
      }
      return text + ' at position ' + position + ' (line ' + line + ' column ' + (position - lineStart + 1) + ')';
    }
    function unexpected(position) {
      if (named[source] === true) return '"' + source + '" is not valid JSON';
      var context = '"' + source + '"';
      if (length > 20) {
        var start = position < 10 ? 0 : position - 10;
        var end = position + 10 < length ? position + 10 : length;
        context = (position < 10 ? '' : '...') + '"' + apply(stringSlice, source, [start, end]) + '"' + (position + 10 < length ? '...' : '');
      }
      return "Unexpected token '" + apply(charAt, source, [position]) + "', " + context + ' is not valid JSON';
    }
    var end = 'Unexpected end of JSON input';

    // string reads a string from the quote at at, and answers a fault or
    // undefined.
    function string() {
      at++;
      for (;;) {
        if (at >= length) return positioned('Unterminated string in JSON', at);
        var c = code(at);
        if (c === 34) {
          at++;
          return undefined;
        }
        if (c < 32) return positioned('Bad control character in string literal in JSON', at);
        if (c !== 92) {
          at++;
          continue;
        }
        at++;
        if (at >= length) return end;
        c = code(at);
        if (c === 117) {
          at++;
          for (var digit = 0; digit < 4; digit++, at++) {
            var h = code(at);
            if (!(isDigit(h) || (h >= 65 && h <= 70) || (h >= 97 && h <= 102))) return positioned('Bad Unicode escape in JSON', at);
          }
        } else if (c === 34 || c === 92 || c === 47 || c === 98 || c === 102 || c === 110 || c === 114 || c === 116) {
          at++;
        } else {
          return positioned('Bad escaped character in JSON', at);
        }
      }
    }

    function number() {
      if (code(at) === 45) {
        at++;
        if (!isDigit(code(at))) return positioned('No number after minus sign in JSON', at);
      }
      if (code(at) === 48) {
        at++;
        if (isDigit(code(at))) return positioned('Unexpected number in JSON', at);
      } else {
        while (isDigit(code(at))) at++;
      }
      if (code(at) === 46) {
        at++;
        if (!isDigit(code(at))) return positioned('Unterminated fractional number in JSON', at);
        while (isDigit(code(at))) at++;
      }
      if (code(at) === 101 || code(at) === 69) {
        at++;
        if (code(at) === 43 || code(at) === 45) at++;
        if (!isDigit(code(at))) return positioned('Exponent part is missing a number in JSON', at);
        while (isDigit(code(at))) at++;
      }
      return undefined;
    }

    function literal(word) {
      for (var index = 0; index < word.length; index++, at++) {
        if (at >= length) return end;
        if (code(at) !== apply(charCodeAt, word, [index])) return unexpected(at);
      }
      return undefined;
    }

    // value reads one value, or opens an object or an array, and answers a
    // fault or undefined.
    function value() {
      skip();
      if (at >= length) return end;
      var c = code(at);
      if (c === 34) return string();
      if (c === 45 || isDigit(c)) return number();
      if (c === 116) return literal('true');
      if (c === 102) return literal('false');
      if (c === 110) return literal('null');
      if (c === 123) {
        at++;
        skip();
        if (code(at) === 125) {
          at++;
          return undefined;
        }
        if (code(at) !== 34) return positioned("Expected property name or '}' in JSON", at);
        open[depth++] = 123;
        return member();
      }
      if (c === 91) {
        at++;
        skip();
        if (code(at) === 93) {
          at++;
          return undefined;
        }
        open[depth++] = 91;
        return value();
      }
      return unexpected(at);
    }

    // member reads a property name, its colon and its value.
    function member() {
      var fault = string();
      if (fault !== undefined) return fault;
      skip();
      if (code(at) !== 58) return positioned("Expected ':' after property name in JSON", at);
      at++;
      return value();
    }

    var fault = value();
    while (fault === undefined) {
      skip();
      if (depth === 0) {
        if (at < length) fault = positioned('Unexpected non-whitespace character after JSON', at);
        break;
      }
      var c = code(at);
      if (open[depth - 1] === 123) {
        if (c === 44) {
          at++;
          skip();
          fault = code(at) === 34 ? member() : positioned('Expected double-quoted property name in JSON', at);
        } else if (c === 125) {
          at++;
          depth--;
        } else {
          fault = positioned("Expected ',' or '}' after property value in JSON", at);
        }
      } else if (c === 44) {
        at++;
        fault = value();
      } else if (c === 93) {
        at++;
        depth--;
      } else {
        fault = positioned("Expected ',' or ']' after array element in JSON", at);
      }
    }
    return fault;
  }

  // parse is JSON.parse, with V8's message when the text is not JSON. A
  // SyntaxError the reviver threw is passed on as it is: the text was valid.
  function parse(text, reviver) {
    var source = `${text}`;
    try {
      return apply(parseJSON, JSON, [source, reviver]);
    } catch (error) {
      if (error instanceof SyntaxErrorType) {
        var fault = jsonFault(source);
        if (fault !== undefined) throw asBuiltIn(new SyntaxErrorType(fault));
      }
      throw error;
    }
  }

  // asBuiltIn shows the stand-in's own frame in an error's stack as the
  // built-in frame it stands for, so the console shows the code calling a
  // built-in parse, as it did before, and not a frame of the runtime's.
  var ownFrame = /\n\tat parse \(kilasflow:errors:[^\n]*/;
  function asBuiltIn(error) {
    var stack = error.stack;
    defineProperty(error, 'stack', {
      value: apply(stringReplace, stack, [ownFrame, '\n\tat parse (native)']), writable: true, enumerable: false, configurable: true,
    });
    return error;
  }
  defineProperty(JSON, 'parse', { value: parse, writable: true, enumerable: false, configurable: true });

  // ---- goja's TypeErrors ----------------------------------------------------------

  // The goja messages reword knows: a property read of undefined or null,
  // and a call or construction of something that is not a function.
  var engineWording = /^(?:Cannot read property '|Object has no member '|Value is not an object: |Not a function: |Value is not a constructor$)/;

  // reword gives an error the engine threw V8's words, when the runtime
  // knows them: see internal/jsrun/wording.go. Anything else it leaves as it
  // is. It never throws: a script can only spoil its own error with the
  // getters and traps it reaches.
  function reword(error) {
    try {
      if (!(error instanceof TypeErrorType)) return;
      var descriptor = getOwnPropertyDescriptor(error, 'message');
      if (descriptor === undefined || !apply(hasOwnProperty, descriptor, ['value']) || typeof descriptor.value !== 'string') return;
      var message = descriptor.value;
      if (!apply(regExpTest, engineWording, [message])) return;
      var header = 'TypeError: ' + message;
      var stack = error.stack;
      if (typeof stack !== 'string' || apply(stringIndexOf, stack, [header]) !== 0) return;
      var frames = apply(stringSlice, stack, [header.length]);
      var words = kit.reword(message, frames);
      if (typeof words !== 'string' || words === '' || words === message) return;
      defineProperty(error, 'message', { value: words, writable: true, enumerable: false, configurable: true });
      defineProperty(error, 'stack', { value: 'TypeError: ' + words + frames, writable: true, enumerable: false, configurable: true });
    } catch (_) {
      // The error stays as it was.
    }
  }

  return { reword: reword };
})

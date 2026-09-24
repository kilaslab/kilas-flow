// KilasFlow's own code, written for internal/jsrun from n8n's documented
// Code-node helpers and the observed behaviour of its HTTP helper. It is not
// derived from n8n's source.
//
// this.helpers.httpRequest, getBinaryDataBuffer and prepareBinaryData, and
// $getWorkflowStaticData. Every helper is one asynchronous call to the
// server: this module turns n8n's arguments into a plain request, the server
// does the work (the request is sent from the server under its egress
// policy, and files are read and stored there), and the answer is turned
// back into what n8n returns. Nothing here reaches the network or a file.
(function (kit) {
  'use strict';

  var global = kit.global;
  var apply = kit.apply;
  var Buffer = global.Buffer;
  var URLSearchParamsType = global.URLSearchParams;
  var bufferFrom = Buffer.from;
  var isBuffer = Buffer.isBuffer;
  var ErrorType = Error;
  var TypeErrorType = TypeError;
  var PromiseType = Promise;
  var promiseThen = Promise.prototype.then;
  var promiseReject = Promise.reject;
  var ArrayBufferType = ArrayBuffer;
  var Uint8ArrayType = Uint8Array;
  var isView = ArrayBuffer.isView;
  var isArray = Array.isArray;
  var keys = Object.keys;
  var hasOwnProperty = Object.prototype.hasOwnProperty;
  var stringifyJSON = JSON.stringify;
  var parseJSON = JSON.parse;
  var encodeComponent = encodeURIComponent;
  var StringType = String;
  var NumberType = Number;
  var isInteger = Number.isInteger;
  var DateType = Date;
  var toISOString = Date.prototype.toISOString;
  var toUpperCase = String.prototype.toUpperCase;
  var toLowerCase = String.prototype.toLowerCase;
  var replace = String.prototype.replace;
  var indexOf = String.prototype.indexOf;
  var arrayIndexOf = Array.prototype.indexOf;
  var join = Array.prototype.join;
  var push = Array.prototype.push;
  var searchParamsToString = URLSearchParamsType.prototype.toString;
  var bufferToString = Buffer.prototype.toString;
  var regExpTest = RegExp.prototype.test;
  var ceil = Math.ceil;

  function own(object, key) {
    return apply(hasOwnProperty, object, [key]);
  }

  function rejected(error) {
    return apply(promiseReject, PromiseType, [error]);
  }

  function refused(subject) {
    return new ErrorType(kit.refusal(subject));
  }

  // ask makes one call to the server and hands its answer to then.
  function ask(method, request, bytes, then) {
    request.method = method;
    return apply(promiseThen, kit.hostCall(stringifyJSON(request), bytes), [then]);
  }

  // ---- httpRequest -------------------------------------------------------------

  // Options n8n's helper honours that this server does not: each would change
  // what the request does, so each is refused rather than ignored. An option
  // n8n does not know at all is ignored, as n8n ignores it.
  var refusedOptions = ['proxy', 'skipSslCertificateValidation', 'abortSignal', 'agentOptions', 'allowedDomains'];
  var encodings = { arraybuffer: true, json: true, text: true };
  var bodiless = { GET: true, HEAD: true, OPTIONS: true };
  var arrayFormats = { indices: true, brackets: true, repeat: true, comma: true };

  function isBytes(value) {
    return value instanceof ArrayBufferType || isView(value);
  }

  function bytesOf(value) {
    if (value instanceof ArrayBufferType) return new Uint8ArrayType(value);
    if (isView(value)) return new Uint8ArrayType(value.buffer, value.byteOffset, value.byteLength);
    return apply(bufferFrom, Buffer, [StringType(value), 'utf8']);
  }

  // headerNamed finds a header without regard to case.
  function headerNamed(headers, name) {
    for (var index = 0; index < headers.length; index++) {
      if (apply(toLowerCase, headers[index][0], []) === name) return index;
    }
    return -1;
  }

  // absolute is a URL with a scheme, or one that starts with //.
  function absolute(url) {
    return apply(regExpTest, /^([a-z][a-z\d+\-.]*:)?\/\//i, [url]);
  }

  // query encodes qs as the helper's default serializer does: arrays as
  // key[]=value, objects as key[inner]=value, dates as ISO strings, and null
  // and undefined left out. arrayFormat chooses another spelling for arrays.
  function query(qs, arrayFormat) {
    var pairs = [];
    function add(key, value) {
      if (value === undefined || value === null) return;
      if (value instanceof DateType) value = apply(toISOString, value, []);
      if (isArray(value)) {
        if (arrayFormat === 'comma') {
          apply(push, pairs, [encodeComponent(key) + '=' + encodeComponent(apply(join, value, [',']))]);
          return;
        }
        for (var index = 0; index < value.length; index++) {
          var name = arrayFormat === 'repeat' ? key : arrayFormat === 'indices' ? key + '[' + index + ']' : key + '[]';
          add(name, value[index]);
        }
        return;
      }
      if (typeof value === 'object') {
        var names = keys(value);
        for (var at = 0; at < names.length; at++) add(key + '[' + names[at] + ']', value[names[at]]);
        return;
      }
      apply(push, pairs, [encodeComponent(key) + '=' + encodeComponent(StringType(value))]);
    }
    var names = keys(qs);
    for (var index = 0; index < names.length; index++) add(names[index], qs[names[index]]);
    return apply(join, pairs, ['&']);
  }

  function emptyObject(value) {
    return value !== null && typeof value === 'object' && !isBytes(value) && keys(value).length === 0;
  }

  // build turns n8n's options into the request the server sends and the
  // settings that decide what the code gets back.
  function build(options) {
    if (options === null || typeof options !== 'object') {
      throw new TypeErrorType('this.helpers.httpRequest needs an options object with a url');
    }
    for (var index = 0; index < refusedOptions.length; index++) {
      var name = refusedOptions[index];
      if (options[name] !== undefined && options[name] !== null && options[name] !== false) {
        throw refused('uses the httpRequest option "' + name + '"');
      }
    }
    var encoding = options.encoding;
    if (encoding !== undefined && encoding !== null && !own(encodings, encoding)) {
      throw refused('uses the httpRequest encoding "' + StringType(encoding) + '"');
    }
    var arrayFormat = options.arrayFormat;
    if (arrayFormat !== undefined && !own(arrayFormats, arrayFormat)) {
      throw new TypeErrorType('arrayFormat must be indices, brackets, repeat or comma');
    }
    if (options.url === undefined || options.url === null || options.url === '') {
      throw new TypeErrorType('this.helpers.httpRequest needs a url');
    }
    var url = StringType(options.url);
    if (options.baseURL !== undefined && options.baseURL !== null && !absolute(url)) {
      url = apply(replace, StringType(options.baseURL), [/\/+$/, '']) + '/' + apply(replace, url, [/^\/+/, '']);
    }
    var method = apply(toUpperCase, StringType(options.method || 'GET'), []);

    var headers = [];
    var given = options.headers;
    if (given !== null && typeof given === 'object') {
      var names = keys(given);
      for (var at = 0; at < names.length; at++) {
        var value = given[names[at]];
        if (value === undefined || value === null) continue;
        apply(push, headers, [[names[at], isArray(value) ? apply(join, value, [', ']) : StringType(value)]]);
      }
    }
    var auth = options.auth;
    if (auth !== null && typeof auth === 'object' && headerNamed(headers, 'authorization') < 0) {
      var pair = StringType(auth.username === undefined ? '' : auth.username) + ':' + StringType(auth.password === undefined ? '' : auth.password);
      var basic = apply(bufferToString, apply(bufferFrom, Buffer, [pair, 'utf8']), ['base64']);
      apply(push, headers, [['Authorization', 'Basic ' + basic]]);
    }
    if (options.json && headerNamed(headers, 'accept') < 0) {
      apply(push, headers, [['Accept', 'application/json']]);
    }

    if (options.qs !== null && typeof options.qs === 'object') {
      var encoded = query(options.qs, arrayFormat);
      if (encoded !== '') url += (apply(indexOf, url, ['?']) < 0 ? '?' : '&') + encoded;
    }

    // A GET never carries a body, and nor does a HEAD or OPTIONS with an empty
    // one. A non-empty object is sent as JSON, or as a form when the code set
    // that content type; a string or bytes as they are; anything else not
    // at all.
    var body = options.body;
    var bytes;
    var contentType = headerNamed(headers, 'content-type');
    var empty = body === undefined || body === null || body === '' || emptyObject(body) ||
      (isBytes(body) && body.byteLength === 0);
    if (method !== 'GET' && !(own(bodiless, method) && empty) && body !== undefined && body !== null) {
      if (isBytes(body)) {
        bytes = bytesOf(body);
      } else if (body instanceof URLSearchParamsType) {
        bytes = bytesOf(apply(searchParamsToString, body, []));
        if (contentType < 0) apply(push, headers, [['Content-Type', 'application/x-www-form-urlencoded']]);
      } else if (typeof body === 'string') {
        bytes = bytesOf(body);
      } else if (typeof body === 'object' && !emptyObject(body)) {
        if (contentType >= 0 && apply(toLowerCase, headers[contentType][1], []) === 'application/x-www-form-urlencoded') {
          bytes = bytesOf(apply(searchParamsToString, new URLSearchParamsType(body), []));
        } else {
          bytes = bytesOf(stringifyJSON(body));
          if (contentType < 0) apply(push, headers, [['Content-Type', 'application/json']]);
        }
      }
    }

    var request = { method: method, url: url, headers: headers };
    var timeout = NumberType(options.timeout);
    if (timeout > 0) request.timeout = ceil(timeout);
    if (options.disableFollowRedirect === true) {
      request.redirects = 0;
    } else if (options.maxRedirects !== undefined && isInteger(options.maxRedirects) && options.maxRedirects >= 0) {
      request.redirects = options.maxRedirects;
    }
    return { request: { http: request }, bytes: bytes, options: options, encoding: encoding };
  }

  // decode reads a response body as the helper does: bytes for arraybuffer,
  // text for text, and otherwise text that is taken as JSON when it parses.
  function decode(data, encoding) {
    var buffer = apply(bufferFrom, Buffer, [data]);
    if (encoding === 'arraybuffer') return buffer;
    var text = apply(bufferToString, buffer, ['utf8']);
    if (encoding === 'text' || text === '') return text;
    try {
      return parseJSON(text);
    } catch (_) {
      return text;
    }
  }

  // passes applies ignoreHttpStatusErrors: true takes every status, and
  // { except: [...] } every status but those; otherwise only a 2xx passes.
  function passes(status, ignore) {
    if (ignore === true) return true;
    if (ignore !== null && typeof ignore === 'object' && isArray(ignore.except)) {
      return apply(arrayIndexOf, ignore.except, [status]) < 0;
    }
    return status >= 200 && status < 300;
  }

  function httpRequest(options) {
    var built;
    try {
      built = build(options);
    } catch (error) {
      return rejected(error);
    }
    return ask('httpRequest', built.request, built.bytes, function (answer) {
      var response = answer.response;
      var body = decode(answer.data, built.encoding);
      if (!passes(response.statusCode, built.options.ignoreHttpStatusErrors)) {
        var error = new ErrorType('The request failed with status ' + response.statusCode +
          (response.statusMessage ? ' ' + response.statusMessage : ''));
        error.status = response.statusCode;
        error.response = { status: response.statusCode, statusText: response.statusMessage, headers: response.headers, data: body };
        throw error;
      }
      if (built.options.returnFullResponse) {
        return { body: body, headers: response.headers, statusCode: response.statusCode, statusMessage: response.statusMessage };
      }
      return body;
    });
  }

  // ---- Files -------------------------------------------------------------------

  // getBinaryDataBuffer reads a file of the node's own input: the one input
  // item itemIndex holds under propertyName.
  function getBinaryDataBuffer(itemIndex, propertyName) {
    if (propertyName !== null && typeof propertyName === 'object') {
      return rejected(refused('passes a file object rather than a property name to this.helpers.getBinaryDataBuffer'));
    }
    var index = NumberType(itemIndex);
    if (!isInteger(index) || index < 0) {
      return rejected(new TypeErrorType('the item index must be a whole number, not ' + StringType(itemIndex)));
    }
    return ask('getBinaryDataBuffer', { itemIndex: index, property: StringType(propertyName) }, undefined, function (answer) {
      return apply(bufferFrom, Buffer, [answer.data]);
    });
  }

  // prepareBinaryData stores bytes as a file of this execution and returns
  // the reference an item's binary holds, which the node may return.
  function prepareBinaryData(data, fileName, mimeType) {
    if (!isBuffer(data) && !isBytes(data)) {
      return rejected(new TypeErrorType('this.helpers.prepareBinaryData needs a Buffer'));
    }
    var request = {};
    if (fileName !== undefined && fileName !== null) request.fileName = StringType(fileName);
    if (mimeType !== undefined && mimeType !== null) request.mimeType = StringType(mimeType);
    return ask('prepareBinaryData', request, bytesOf(data), function (answer) {
      return answer.file;
    });
  }

  kit.helpers.httpRequest = httpRequest;
  kit.helpers.getBinaryDataBuffer = getBinaryDataBuffer;
  kit.helpers.prepareBinaryData = prepareBinaryData;

  // ---- Static data -------------------------------------------------------------

  // $getWorkflowStaticData hands out one object per kind for the whole run:
  // the workflow's shared data for 'global', this node's own for 'node'.
  // What the objects hold when the code finishes is what the server keeps.
  var handedOut = {};
  kit.define('$getWorkflowStaticData', function $getWorkflowStaticData(type) {
    if (type !== 'global' && type !== 'node') {
      throw new ErrorType("$getWorkflowStaticData takes 'global' or 'node', not " +
        (typeof type === 'string' ? "'" + type + "'" : StringType(type)));
    }
    if (!own(handedOut, type)) handedOut[type] = parseJSON(kit.staticData(type));
    return handedOut[type];
  });

  return {
    staticData: function () { return handedOut; },
  };
})

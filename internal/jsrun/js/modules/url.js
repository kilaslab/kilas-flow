// KilasFlow's own code, written for internal/jsrun from Node's documented url
// module. It is not derived from Node's or n8n's source.
//
// require('url') answers with the WHATWG URL and URLSearchParams that
// goja_nodejs installs as globals. Node's legacy url.parse and url.format are
// not provided; new URL() is the documented replacement.
(function (kit) {
  'use strict';
  return { URL: kit.global.URL, URLSearchParams: kit.global.URLSearchParams };
})

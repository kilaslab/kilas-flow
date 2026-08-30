package api

import (
	"html/template"
	"net/http"
)

// ScalarBundlePath is where the vendored Scalar bundle is served from. It is
// copied into the SPA's static assets by web/scripts/vendor-docs.mjs and
// embedded alongside the rest of the frontend.
const ScalarBundlePath = "/vendor/scalar.js"

// docsTemplate renders the API reference.
//
// Huma's built-in renderer loads Scalar from unpkg. kilasflow serves its own page
// instead so /docs works with no network: the binary is meant to be
// self-contained, and it gets embedded into other companies' products where a
// third-party CDN in the request path is both an availability risk and a
// privacy one.
var docsTemplate = template.Must(template.New("docs").Parse(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <meta name="referrer" content="no-referrer" />
    <title>{{.Title}}</title>
    <link rel="icon" href="/favicon.svg" />
    <style>
      body { margin: 0; }
      /* Shown only if the bundle fails to load, which means the SPA assets
         were not built into the binary. */
      #docs-fallback { display: none; }
      body.docs-failed #docs-fallback {
        display: block;
        max-width: 34rem;
        margin: 4rem auto;
        padding: 0 1.5rem;
        font: 15px/1.6 ui-sans-serif, system-ui, sans-serif;
      }
      code { background: #eee; padding: .15em .4em; border-radius: 4px; }
    </style>
  </head>
  <body>
    <div id="docs-fallback">
      <h1>API reference unavailable</h1>
      <p>
        The documentation bundle was not found at <code>{{.BundlePath}}</code>.
        It is copied from <code>node_modules</code> into the frontend build, so
        this usually means the binary was compiled without a frontend build.
      </p>
      <p>Run <code>make build-all</code>, or read the raw specification:</p>
      <ul>
        <li><a href="{{.SpecPath}}">{{.SpecPath}}</a></li>
        <li><a href="{{.SpecPathYAML}}">{{.SpecPathYAML}}</a></li>
      </ul>
    </div>

    <script
      id="api-reference"
      data-url="{{.SpecPath}}"
      data-configuration='{{.Configuration}}'></script>
    <script src="{{.BundlePath}}" onerror="document.body.classList.add('docs-failed')"></script>
  </body>
</html>
`))

type docsData struct {
	Title         string
	SpecPath      string
	SpecPathYAML  string
	BundlePath    string
	Configuration template.JS
}

// docsConfiguration is Scalar's inline configuration.
//
// withDefaultFonts:false stops it requesting webfonts from fonts.scalar.com.
// The CSP would block them anyway, but suppressing the attempt keeps the
// browser console clean instead of logging fourteen violations per page load.
const docsConfiguration = `{"withDefaultFonts":false,"hideModels":false}`

// docsCSP confines the documentation page to same-origin resources.
//
// Vendoring the bundle is necessary but not sufficient: at runtime Scalar still
// fetches webfonts from fonts.scalar.com and queries api.scalar.com for its
// registry and search features. connect-src and font-src block both, so the
// page works offline and leaks nothing about which APIs are being browsed.
// Fonts fall back to the system stack.
//
// unsafe-inline is required for styles because Scalar injects them at runtime,
// and for scripts because the page carries an inline configuration element.
const docsCSP = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline' 'unsafe-eval'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; " +
	"font-src 'self' data:; " +
	"connect-src 'self'; " +
	"frame-ancestors 'self'; " +
	"base-uri 'self'"

// docsHandler renders the self-hosted API reference.
func docsHandler(title string) http.Handler {
	data := docsData{
		Title:        title + " Reference",
		SpecPath:     OpenAPIPath + ".json",
		SpecPathYAML: OpenAPIPath + ".yaml",
		BundlePath:   ScalarBundlePath,

		Configuration: template.JS(docsConfiguration),
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", docsCSP)
		w.Header().Set("Referrer-Policy", "no-referrer")

		if err := docsTemplate.Execute(w, data); err != nil {
			http.Error(w, "failed to render docs", http.StatusInternalServerError)
		}
	})
}

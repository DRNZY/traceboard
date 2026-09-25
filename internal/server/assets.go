package server

import (
	"io"
	"net/http"
	"strings"
)

type readSeeker interface {
	io.ReadSeeker
}

// signInPageHTML renders the one-time exchange form. The one-time token is
// placed in a form field, never in a script, and the stylesheet is a separate
// same-origin request, so the strict Content Security Policy needs no exception
// and the page loads no external asset.
func signInPageHTML(token, message string) string {
	var builder strings.Builder
	builder.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	builder.WriteString(`<meta name="viewport" content="width=device-width,initial-scale=1">`)
	builder.WriteString(`<title>Traceboard sign in</title>`)
	builder.WriteString(`<link rel="stylesheet" href="/auth/signin.css">`)
	builder.WriteString(`</head><body><main>`)
	builder.WriteString(`<h1>Traceboard sign in</h1>`)
	if message != "" {
		builder.WriteString(`<p class="note">`)
		builder.WriteString(htmlEscape(message))
		builder.WriteString(`</p>`)
	}
	builder.WriteString(`<form method="post" action="/auth/session">`)
	builder.WriteString(`<label for="token">One-time token</label>`)
	builder.WriteString(`<input id="token" name="token" autocomplete="off" spellcheck="false" value="`)
	builder.WriteString(htmlEscape(token))
	builder.WriteString(`">`)
	builder.WriteString(`<button type="submit">Open dashboard</button>`)
	builder.WriteString(`</form></main></body></html>`)
	return builder.String()
}

// signInStylesheet is served from this process. The sign-in page is reachable
// before any session exists, so it cannot use the embedded dashboard assets.
const signInStylesheet = `body{margin:0;background:#0b0b0b;color:#f2efe6;font:16px/1.5 ui-monospace,'Courier New',monospace;display:grid;min-height:100dvh;place-items:center}
main{width:min(92vw,34rem);border:2px solid #f2efe6;padding:2rem}
h1{font-size:1.5rem;letter-spacing:-0.02em;margin:0 0 1rem;text-transform:uppercase}
p{margin:0 0 1rem}
label{display:block;margin:0 0 0.5rem;text-transform:uppercase;font-size:0.75rem;letter-spacing:0.08em}
input{width:100%;padding:0.75rem;background:#0b0b0b;color:#f2efe6;border:2px solid #f2efe6;font:inherit}
input:focus-visible{outline:4px solid #d9241c;outline-offset:2px}
button{margin-top:1rem;width:100%;padding:0.75rem;background:#d9241c;color:#f2efe6;border:2px solid #0b0b0b;font:700 1rem/1 inherit;text-transform:uppercase;letter-spacing:0.08em;cursor:pointer}
button:focus-visible{outline:4px solid #f2efe6;outline-offset:2px}
.note{color:#d9241c;font-size:0.8125rem}`

func handleSignInStylesheet(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "text/css; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(writer, signInStylesheet)
}

// renderSignInFailure re-renders the sign-in page with a fixed message. It never
// echoes the submitted token, so a failed exchange leaves no credential on the
// page or in the browser history.
func renderSignInFailure(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusUnauthorized)
	_, _ = io.WriteString(writer, signInPageHTML("", "That sign-in link is no longer valid. Run traceboard start to print a new one."))
}

func htmlEscape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&#34;",
		"'", "&#39;",
	)
	return replacer.Replace(value)
}

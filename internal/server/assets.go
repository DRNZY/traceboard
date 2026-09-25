package server

import (
	"io"
	"net/http"
	"strings"
)

type readSeeker interface {
	io.ReadSeeker
}

// signInPageHTML renders the one-time exchange form. The token is placed in a
// form field, never in a script, so the strict Content Security Policy stays
// intact and no external asset is ever loaded.
func signInPageHTML(token, message string) string {
	var builder strings.Builder
	builder.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	builder.WriteString(`<meta name="viewport" content="width=device-width,initial-scale=1">`)
	builder.WriteString(`<title>Traceboard sign in</title><style>`)
	builder.WriteString(`body{margin:0;background:#0b0b0b;color:#f4f1ea;font:16px/1.5 ui-monospace,'Courier New',monospace;display:grid;min-height:100dvh;place-items:center}`)
	builder.WriteString(`main{width:min(92vw,34rem);border:2px solid #f4f1ea;padding:2rem}`)
	builder.WriteString(`h1{font-size:1.5rem;letter-spacing:-0.02em;margin:0 0 1rem;text-transform:uppercase}`)
	builder.WriteString(`p{margin:0 0 1rem}`)
	builder.WriteString(`label{display:block;margin:0 0 0.5rem;text-transform:uppercase;font-size:0.75rem;letter-spacing:0.08em}`)
	builder.WriteString(`input{width:100%;padding:0.75rem;background:#0b0b0b;color:#f4f1ea;border:2px solid #f4f1ea;font:inherit}`)
	builder.WriteString(`input:focus-visible{outline:4px solid #e02b20;outline-offset:2px}`)
	builder.WriteString(`button{margin-top:1rem;width:100%;padding:0.75rem;background:#e02b20;color:#0b0b0b;border:2px solid #0b0b0b;font:700 1rem/1 inherit;text-transform:uppercase;letter-spacing:0.08em;cursor:pointer}`)
	builder.WriteString(`button:focus-visible{outline:4px solid #f4f1ea;outline-offset:2px}`)
	builder.WriteString(`.note{color:#e02b20;font-size:0.8125rem}</style></head><body><main>`)
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

// renderSignInFailure re-renders the sign-in page with a fixed message. It never
// echoes the submitted token, so a failed exchange leaves no credential on the
// page or in the browser history.
func renderSignInFailure(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusUnauthorized)
	_, _ = io.WriteString(writer, signInPageHTML("", "That sign-in link is no longer valid. Run traceboard start to print a new one."))
}

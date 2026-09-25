package server

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"traceboard/internal/auth"
)

const (
	maxIngestBodyBytes    = 4 << 20
	maxHookBodyBytes      = 1 << 20
	maxQueryBodyBytes     = 64 << 10
	allowedHostSuffixDNS  = "localhost"
	contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self' ws://127.0.0.1:* ws://localhost:*; form-action 'self'; frame-ancestors 'none'; base-uri 'none'; object-src 'none'"
)

type middleware struct {
	auth    *auth.Manager
	trusted map[string]struct{}
}

func newMiddleware(manager *auth.Manager, listenAddress string) middleware {
	trusted := map[string]struct{}{}
	host, _, err := net.SplitHostPort(listenAddress)
	if err == nil && host != "" {
		trusted[host] = struct{}{}
	}
	trusted["localhost"] = struct{}{}
	trusted["127.0.0.1"] = struct{}{}
	trusted["[::1]"] = struct{}{}
	trusted["::1"] = struct{}{}
	return middleware{auth: manager, trusted: trusted}
}

func (m middleware) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		header := writer.Header()
		header.Set("Content-Security-Policy", contentSecurityPolicy)
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), interest-cohort=()")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Cross-Origin-Opener-Policy", "same-origin")
		header.Set("Cross-Origin-Resource-Policy", "same-origin")
		header.Set("Cache-Control", "no-store")
		next.ServeHTTP(writer, request)
	})
}

// validateHost rejects DNS rebinding: a request that reached a loopback
// listener must still name a loopback host.
func (m middleware) validateHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		host := request.Host
		if index := strings.LastIndex(host, ":"); index > 0 && !strings.Contains(host[index:], "]") {
			host = host[:index]
		}
		host = strings.Trim(host, "[]")
		if _, ok := m.trusted[host]; !ok {
			if !strings.EqualFold(host, allowedHostSuffixDNS) {
				writeError(writer, http.StatusMisdirectedRequest, "invalid_host", "request host is not permitted")
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}

// requireSameOrigin blocks cross-site mutations from a browser.
//
// Two signals are used together. `Origin` is the primary one, but a form
// submission from a document the browser treats as having an opaque origin
// sends the literal value "null", which is not comparable. `Sec-Fetch-Site` is
// the browser-supplied fallback: page script cannot set it, so it is
// trustworthy when present. A client that sends neither is not a browser and
// holds no ambient cookie, so there is nothing for it to ride on.
func (m middleware) requireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		if origin == "" || origin == "null" {
			if request.Header.Get("Sec-Fetch-Site") == "cross-site" {
				writeError(writer, http.StatusForbidden, "cross_origin", "cross-origin request rejected")
				return
			}
			next.ServeHTTP(writer, request)
			return
		}
		parsed, err := url.Parse(origin)
		if err != nil {
			writeError(writer, http.StatusForbidden, "cross_origin", "cross-origin request rejected")
			return
		}
		host := parsed.Hostname()
		if host == "localhost" {
			next.ServeHTTP(writer, request)
			return
		}
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			writeError(writer, http.StatusForbidden, "cross_origin", "cross-origin request rejected")
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (m middleware) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cookie, err := request.Cookie(auth.SessionCookieName)
		if err != nil || cookie.Value == "" {
			writeError(writer, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		if err := m.auth.ValidateSession(request.Context(), cookie.Value); err != nil {
			writeError(writer, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (m middleware) requireIngestToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		presented := bearerToken(request)
		if presented == "" {
			writeError(writer, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		if err := m.auth.ValidateIngestToken(presented); err != nil {
			writeError(writer, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (m middleware) limitBody(limit int64, tooLarge func(http.ResponseWriter)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.ContentLength > limit {
				tooLarge(writer)
				return
			}
			if request.Body != nil {
				request.Body = http.MaxBytesReader(writer, request.Body, limit)
			}
			next.ServeHTTP(writer, request)
		})
	}
}

func bearerToken(request *http.Request) string {
	header := request.Header.Get("Authorization")
	if header == "" {
		return ""
	}
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

func constantTimeEqual(left, right string) bool {
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func writeJSON(writer http.ResponseWriter, status int, payload any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	if payload == nil {
		return
	}
	_ = writeJSONBody(writer, payload)
}

func writeError(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func writeJSONBody(writer io.Writer, payload any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(true)
	return encoder.Encode(payload)
}

func parseNonNegative(value string) (int64, bool) {
	if value == "" {
		return 0, true
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, false
	}
	return parsed, true
}

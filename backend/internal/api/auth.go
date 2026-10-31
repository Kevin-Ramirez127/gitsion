package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

const bearerPrefix = "Bearer "

// requireAdmin rejects writes when no admin token is configured (read-only
// mode) or when the request does not carry the configured bearer token.
func (s *server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.adminToken == "" {
			writeError(w, http.StatusForbidden, "writes_disabled",
				"this dashboard is read-only; set ADMIN_TOKEN to enable changes")
			return
		}

		if !tokenEqual(bearerToken(r), s.adminToken) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="gitsion"`)
			writeError(w, http.StatusUnauthorized, "unauthorized", "admin token is missing or invalid")
			return
		}

		next(w, r)
	}
}

// adminSession lets the frontend confirm that a token is accepted before it
// creates a browser session.
func (s *server) adminSession(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func bearerToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(header) < len(bearerPrefix) || !strings.EqualFold(header[:len(bearerPrefix)], bearerPrefix) {
		return ""
	}
	return strings.TrimSpace(header[len(bearerPrefix):])
}

// tokenEqual compares SHA-256 digests in constant time. Hashing first keeps the
// comparison independent of the supplied token's length.
func tokenEqual(got, want string) bool {
	gotHash := sha256.Sum256([]byte(got))
	wantHash := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(gotHash[:], wantHash[:]) == 1
}

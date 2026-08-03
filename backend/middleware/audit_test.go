package middleware

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"khairul169/garage-webui/utils"
)

// TestAuditLog verifies the audit middleware logs state-changing requests
// (with their final status, including denied ones) to stdout and skips reads.
// Requests are served through the scs LoadAndSave middleware so that
// utils.Session.Get has session data in the request context (otherwise scs
// panics); no username is set on the session, so the audit line records "-".
func TestAuditLog(t *testing.T) {
	// Capture the stdlib logger; restore the default (stderr) when done.
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	sessMgr := utils.InitSessionManager() // also sets the package-global utils.Session

	// dummy responds with the given status so we can assert the recorder
	// captures it (including a denied write's 403).
	dummy := func(status int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		})
	}

	t.Run("POST mutation is logged with status and default user", func(t *testing.T) {
		buf.Reset()
		handler := sessMgr.LoadAndSave(AuditLog(dummy(http.StatusOK)))
		req := httptest.NewRequest(http.MethodPost, "/x", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)

		out := buf.String()
		for _, want := range []string{`"audit":true`, `"method":"POST"`, `"path":"/x"`, `"status":200`, `"user":"-"`} {
			if !strings.Contains(out, want) {
				t.Errorf("audit line missing %q\ngot: %s", want, out)
			}
		}
	})

	t.Run("GET read is not logged", func(t *testing.T) {
		buf.Reset()
		handler := sessMgr.LoadAndSave(AuditLog(dummy(http.StatusOK)))
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)

		if out := buf.String(); out != "" {
			t.Errorf("GET should not be audited, but logged: %s", out)
		}
	})

	t.Run("denied DELETE is logged with status 403", func(t *testing.T) {
		buf.Reset()
		handler := sessMgr.LoadAndSave(AuditLog(dummy(http.StatusForbidden)))
		req := httptest.NewRequest(http.MethodDelete, "/browse/b/k", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)

		out := buf.String()
		for _, want := range []string{`"audit":true`, `"method":"DELETE"`, `"status":403`} {
			if !strings.Contains(out, want) {
				t.Errorf("denied-write audit line missing %q\ngot: %s", want, out)
			}
		}
	})
}

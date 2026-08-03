package middleware

import (
	"encoding/json"
	"github.com/d7eeem/garage-webui-ng/utils"
	"log"
	"net/http"
	"time"
)

// statusRecorder captures the response status for the audit line.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// AuditLog emits a structured (JSON) stdout line for every state-changing
// request (POST/PUT/DELETE/PATCH), including denied ones. Reads (GET/HEAD/OPTIONS)
// are not logged. The backend is stateless — this is a stdout trail, not a store.
func AuditLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		user, _ := utils.Session.Get(r, "username").(string)
		if user == "" {
			user = "-"
		}
		entry, _ := json.Marshal(map[string]any{
			"audit":  true,
			"ts":     time.Now().UTC().Format(time.RFC3339),
			"user":   user,
			"method": r.Method,
			"path":   r.URL.Path,
			"status": rec.status,
		})
		log.Println(string(entry))
	})
}

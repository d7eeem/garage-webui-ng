package router

import (
	"github.com/d7eeem/garage-webui-ng/middleware"
	"net/http"
)

func HandleApiRouter() *http.ServeMux {
	mux := http.NewServeMux()

	auth := &Auth{}
	mux.HandleFunc("POST /auth/login", auth.Login)

	router := http.NewServeMux()
	router.HandleFunc("POST /auth/logout", auth.Logout)
	router.HandleFunc("GET /auth/status", auth.GetStatus)
	router.HandleFunc("POST /auth/change-password", auth.ChangePassword)

	// First-run wizard. Registered on the inner router — not on mux — so both
	// routes still pass through AuditLog and AuthMiddleware; the middleware's
	// isPublicPath allowlist is what lets them through without a session, and
	// the handlers carry their own guard against running twice.
	setup := &Setup{}
	router.HandleFunc("GET /setup/status", setup.GetStatus)
	router.HandleFunc("POST /setup", setup.Create)

	// User administration. On the inner router, so every one of these inherits
	// AuditLog, CSRF and AuthMiddleware — the middleware denies /admin/ to a
	// viewer, and each handler calls requireAdmin as a second, independent
	// check. Registering any of them on mux instead would bypass both.
	adminUsers := &AdminUsers{}
	router.HandleFunc("GET /admin/users", adminUsers.List)
	router.HandleFunc("POST /admin/users", adminUsers.Create)
	router.HandleFunc("PATCH /admin/users/{id}", adminUsers.Update)
	router.HandleFunc("DELETE /admin/users/{id}", adminUsers.Delete)
	router.HandleFunc("POST /admin/users/{id}/reset-password", adminUsers.ResetPassword)

	config := &Config{}
	router.HandleFunc("GET /config", config.GetAll)

	// Registered on the inner router, not mux, so it inherits AuthMiddleware —
	// an unauthenticated caller must not be able to make this service emit
	// outbound requests to GitHub.
	update := &Update{}
	router.HandleFunc("GET /update-check", update.Get)

	buckets := &Buckets{}
	router.HandleFunc("GET /buckets", buckets.GetAll)

	browse := &Browse{}
	router.HandleFunc("GET /browse/{bucket}", browse.GetObjects)
	router.HandleFunc("POST /browse/download-token", browse.CreateDownloadToken)
	router.HandleFunc("GET /browse/{bucket}/archive", browse.DownloadArchive)
	router.HandleFunc("GET /browse/{bucket}/{key...}", browse.GetOneObject)
	router.HandleFunc("PUT /browse/{bucket}/{key...}", browse.PutObject)
	router.HandleFunc("DELETE /browse/{bucket}/{key...}", browse.DeleteObject)
	router.HandleFunc("POST /browse/{bucket}", browse.BulkDeleteObjects)

	router.HandleFunc("GET /multipart/{bucket}", browse.ListMultipartUploads)
	router.HandleFunc("DELETE /multipart/{bucket}", browse.AbortMultipartUpload)

	router.HandleFunc("GET /share/{bucket}/{key...}", browse.ShareObject)

	metrics := &Metrics{}
	router.HandleFunc("GET /metrics", metrics.Get)

	// Proxy request to garage api endpoint
	router.HandleFunc("/", ProxyHandler)

	// Order matters. AuditLog is outermost so it records the final status of
	// every write, including the ones rejected below it; the forgery check
	// comes next so a request without a valid token never reaches the session
	// logic; AuthMiddleware is innermost, closest to the handlers.
	//
	// POST /auth/login is registered on the outer mux above and so bypasses all
	// three — it is exempt from the token check in any case, being the one
	// write a caller makes before it has a session.
	mux.Handle("/", middleware.AuditLog(middleware.CSRF(middleware.AuthMiddleware(router))))
	return mux
}

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

	// First-run wizard. Registered on the inner router — not on mux — so both
	// routes still pass through AuditLog and AuthMiddleware; the middleware's
	// isPublicPath allowlist is what lets them through without a session, and
	// the handlers carry their own guard against running twice.
	setup := &Setup{}
	router.HandleFunc("GET /setup/status", setup.GetStatus)
	router.HandleFunc("POST /setup", setup.Create)

	config := &Config{}
	router.HandleFunc("GET /config", config.GetAll)

	buckets := &Buckets{}
	router.HandleFunc("GET /buckets", buckets.GetAll)

	browse := &Browse{}
	router.HandleFunc("GET /browse/{bucket}", browse.GetObjects)
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

	mux.Handle("/", middleware.AuditLog(middleware.AuthMiddleware(router)))
	return mux
}

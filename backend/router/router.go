package router

import (
	"khairul169/garage-webui/middleware"
	"net/http"
)

func HandleApiRouter() *http.ServeMux {
	mux := http.NewServeMux()

	auth := &Auth{}
	mux.HandleFunc("POST /auth/login", auth.Login)

	router := http.NewServeMux()
	router.HandleFunc("POST /auth/logout", auth.Logout)
	router.HandleFunc("GET /auth/status", auth.GetStatus)

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

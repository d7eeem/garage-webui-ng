package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/d7eeem/garage-webui-ng/router"
	"github.com/d7eeem/garage-webui-ng/ui"
	"github.com/d7eeem/garage-webui-ng/utils"

	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load()

	// -health runs a lightweight self-probe used by the container HEALTHCHECK:
	// it issues a local HTTP request and exits 0 (healthy) or 1 (unhealthy), so
	// the runtime image needs no shell or curl.
	healthCheck := flag.Bool("health", false, "run a local health probe and exit (0 = healthy)")
	flag.Parse()
	if *healthCheck {
		os.Exit(runHealthCheck())
	}

	// Initialize app
	utils.InitCacheManager()
	sessionMgr := utils.InitSessionManager()

	if err := utils.Garage.LoadConfig(); err != nil {
		log.Println("Cannot load garage config!", err)
	}

	if utils.GetEnv("AUTH_USER_PASS", "") == "" {
		log.Println("WARNING: AUTH_USER_PASS is not set — the web UI and the " +
			"Garage admin API proxy are accessible without authentication. " +
			"Set AUTH_USER_PASS or restrict network access to this port.")
	}

	basePath := os.Getenv("BASE_PATH")
	mux := http.NewServeMux()

	// Serve API
	apiPrefix := basePath + "/api"
	mux.Handle(apiPrefix+"/", http.StripPrefix(apiPrefix, router.HandleApiRouter()))

	// Static files
	ui.ServeUI(mux)

	// Redirect to UI if BASE_PATH is set
	if basePath != "" {
		mux.Handle("/", http.RedirectHandler(basePath, http.StatusMovedPermanently))
	}

	host := utils.GetEnv("HOST", "0.0.0.0")
	port := utils.GetEnv("PORT", "3909")
	addr := fmt.Sprintf("%s:%s", host, port)

	srv := &http.Server{
		Addr:    addr,
		Handler: sessionMgr.LoadAndSave(mux),
	}

	// Run the server in the background so the main goroutine can wait for a
	// termination signal and shut down gracefully.
	go func() {
		log.Printf("Starting server on http://%s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	// Graceful shutdown: on SIGINT/SIGTERM stop accepting new connections and
	// give in-flight requests up to 10s to finish.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Graceful shutdown failed: %v", err)
		os.Exit(1)
	}
	log.Println("Server stopped cleanly.")
}

// runHealthCheck probes the local server on the configured PORT (honouring
// BASE_PATH) and returns a process exit code: 0 when the server answers with a
// non-server-error status, 1 otherwise. Used by the Docker HEALTHCHECK.
func runHealthCheck() int {
	port := utils.GetEnv("PORT", "3909")
	basePath := os.Getenv("BASE_PATH")
	url := fmt.Sprintf("http://127.0.0.1:%s%s/", port, basePath)

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "health: request failed:", err)
		return 1
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return 0
	}
	fmt.Fprintln(os.Stderr, "health: unexpected status:", resp.StatusCode)
	return 1
}

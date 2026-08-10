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

	"github.com/d7eeem/garage-webui-ng/middleware"
	"github.com/d7eeem/garage-webui-ng/router"
	"github.com/d7eeem/garage-webui-ng/store"
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

	// Offline account recovery. These operate directly on the SQLite file at
	// DB_PATH and are deliberately not reachable over HTTP — an unauthenticated
	// password-reset endpoint would be a backdoor. Passwords are prompted for,
	// never passed as arguments (argv leaks into shell history and `ps`).
	resetPassword := flag.String("reset-password", "", "set a new password for `username` (prompts; local database access required)")
	createAdmin := flag.String("create-admin", "", "create a new administrator called `username` (prompts)")
	listUsers := flag.Bool("list-users", false, "list accounts in the user database and exit")
	showVersion := flag.Bool("version", false, "print the version and exit")

	flag.Parse()

	// -version is the cheapest possible branch: it must not need a database or
	// a Garage connection, so it runs before anything else.
	if *showVersion {
		fmt.Println(Version())
		return
	}

	// -health stays the first and cheapest branch: it is the container
	// HEALTHCHECK and runs on every probe interval.
	if *healthCheck {
		os.Exit(runHealthCheck())
	}
	switch {
	case *listUsers:
		os.Exit(runListUsers())
	case *resetPassword != "":
		os.Exit(runResetPassword(*resetPassword))
	case *createAdmin != "":
		os.Exit(runCreateAdmin(*createAdmin))
	}

	// Initialize app
	utils.InitCacheManager()
	sessionMgr := utils.InitSessionManager()

	// Package router cannot import package main, so the version it reports
	// through GET /config and GET /update-check is pushed in here.
	router.AppVersion = Version()

	if err := utils.Garage.LoadConfig(); err != nil {
		log.Println("Cannot load garage config!", err)
	}

	basePath := os.Getenv("BASE_PATH")
	host := utils.GetEnv("HOST", "0.0.0.0")
	port := utils.GetEnv("PORT", "3909")
	addr := fmt.Sprintf("%s:%s", host, port)

	// The user database is the one piece of state this service owns. Failing
	// to open it means nobody can log in and authentication is mandatory, so
	// there is nothing useful to serve — fail fast and loudly instead of
	// starting a UI that can only return errors.
	dbPath := store.DBPath()
	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("cannot open user database at %s: %v", dbPath, err)
	}
	defer st.Close()
	store.SetDefault(st)
	log.Printf("User database: %s", dbPath)

	// One-time migration off the legacy environment variables. After this the
	// database is authoritative and AUTH_USER_PASS is ignored forever.
	imported, err := store.ImportLegacyUsers(
		context.Background(), st,
		utils.GetEnv("AUTH_USER_PASS", ""),
		utils.GetEnv("AUTH_VIEWER_USER_PASS", ""),
	)
	if err != nil {
		log.Printf("legacy user import failed: %v", err)
	}
	if imported > 0 {
		log.Printf("Initial administrator imported from AUTH_USER_PASS (%d user(s)).", imported)
	}

	if count, err := st.CountUsers(context.Background()); err != nil {
		log.Printf("cannot count users: %v", err)
	} else if count == 0 {
		log.Printf("No users configured — open http://%s%s/setup to create the first administrator.", addr, basePath)
	}

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

	srv := &http.Server{
		Addr:    addr,
		Handler: middleware.SecurityHeaders(sessionMgr.LoadAndSave(mux)),
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

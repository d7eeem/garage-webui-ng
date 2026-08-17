package router

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/d7eeem/garage-webui-ng/store"
	"github.com/d7eeem/garage-webui-ng/utils"
)

// Setup serves the first-run wizard: the only way to create an account on a
// brand-new deployment, which by definition has nobody who could authenticate
// in order to create one.
//
// Both handlers are reachable without a session (middleware.isPublicPath), so
// each one carries its own guard. GetStatus discloses a single boolean, and
// Create refuses to run once any user exists — atomically, inside
// store.CreateFirstAdmin. Nothing here may ever log the submitted password or
// its hash.
type Setup struct{}

// GetStatus tells the UI whether the wizard still applies. It deliberately
// reveals nothing but that one bit: on a set-up instance the answer is the
// same for every caller, authenticated or not.
func (c *Setup) GetStatus(w http.ResponseWriter, r *http.Request) {
	// A missing store means startup has not finished. Report the deployment as
	// un-set-up rather than as ready — the wizard's own guard, not this
	// answer, is what decides whether an account can actually be created.
	st := store.Default()
	if st == nil {
		utils.ResponseSuccess(w, map[string]any{"needsSetup": true})
		return
	}

	n, err := st.CountUsers(r.Context())
	if err != nil {
		utils.ResponseError(w, fmt.Errorf("cannot read users: %w", err))
		return
	}

	utils.ResponseSuccess(w, map[string]any{"needsSetup": n == 0})
}

// Create makes the first administrator and logs the caller straight in, the
// way Gitea and Grafana do — finishing the wizard should land in the app, not
// on a login form.
//
// The session code below is deliberately identical to Auth.Login, including
// the Renew that stops a pre-planted session ID from becoming an
// authenticated one. Keep the two in sync.
func (c *Setup) Create(w http.ResponseWriter, r *http.Request) {
	// Its own budget (setupAttempts, not the login limiter): an unauthenticated
	// write endpoint should not be a free-running loop for anyone who can reach
	// the port, even before the store guard makes further attempts pointless,
	// and it must not share its allowance with unrelated login attempts.
	if !setupAttempts.allow(clientAddr(r), time.Now()) {
		utils.ResponseErrorStatus(w, errors.New("too many setup attempts, try again later"), http.StatusTooManyRequests)
		return
	}

	var body struct {
		Username        string `json:"username"`
		Password        string `json:"password"`
		ConfirmPassword string `json:"confirmPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		// The decoder error is not echoed back: it can quote the request body,
		// which is exactly where the password is.
		utils.ResponseErrorStatus(w, errors.New("invalid request body"), http.StatusBadRequest)
		return
	}

	if body.Password != body.ConfirmPassword {
		utils.ResponseErrorStatus(w, errors.New("passwords do not match"), http.StatusBadRequest)
		return
	}

	st := store.Default()
	if st == nil {
		utils.ResponseErrorStatus(w, store.ErrNoStore, http.StatusInternalServerError)
		return
	}

	user, err := store.CreateFirstAdmin(r.Context(), st, body.Username, body.Password)
	switch {
	case errors.Is(err, store.ErrSetupAlreadyDone):
		// The security-critical outcome: this instance already has a user, so
		// the wizard is closed for good.
		utils.ResponseErrorStatus(w, err, http.StatusConflict)
		return

	case errors.Is(err, store.ErrWeakPassword),
		errors.Is(err, store.ErrInvalidUsername),
		errors.Is(err, store.ErrUsernameTaken):
		// Validation failures carry a reason the operator needs to see. None
		// of them contains the password.
		utils.ResponseErrorStatus(w, err, http.StatusBadRequest)
		return

	case err != nil:
		utils.ResponseError(w, fmt.Errorf("cannot create the first administrator: %w", err))
		return
	}

	if err := utils.Session.Renew(r); err != nil {
		utils.ResponseErrorStatus(w, errors.New("cannot start session"), http.StatusInternalServerError)
		return
	}

	utils.Session.Set(r, "authenticated", true)
	utils.Session.Set(r, "username", user.Username)
	utils.Session.Set(r, "role", user.Role)

	log.Printf("first-run setup: created administrator %q", user.Username)

	utils.ResponseSuccess(w, map[string]any{
		"authenticated": true,
		"username":      user.Username,
		"role":          user.Role,
	})
}

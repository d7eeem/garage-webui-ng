package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/d7eeem/garage-webui-ng/store"
	"github.com/d7eeem/garage-webui-ng/utils"
)

// AdminUsers serves the user-administration API behind /admin/users. Every
// handler here is privileged: it can create accounts, change roles and delete
// the people who administer this instance.
//
// Two properties are load-bearing and must survive any refactor:
//
//   - Only an administrator may reach these endpoints. There are two
//     independent checks — middleware.isViewerAllowed denies /admin/ outright,
//     and requireAdmin below runs inside every handler.
//   - No response may ever carry a password or a password hash. User.PasswordHash
//     is `json:"-"`, and the tests assert on raw response bodies that no bcrypt
//     prefix appears anywhere. Nothing here logs a submitted password either.
type AdminUsers struct{}

// requireAdmin reports whether the session belongs to an administrator and
// writes a 403 when it does not. Every /admin/* handler must call it first —
// the middleware check in isViewerAllowed is the outer boundary; this is the
// second, so a routing mistake cannot expose these endpoints.
func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if role, _ := utils.Session.Get(r, "role").(string); role == store.RoleAdmin {
		return true
	}
	utils.ResponseErrorStatus(w, errors.New("forbidden: administrator role required"), http.StatusForbidden)
	return false
}

// sessionUsername is the login name the caller's session carries. It is the
// only identity the self-modification guards trust: the request body never
// says who the caller is.
func sessionUsername(r *http.Request) string {
	name, _ := utils.Session.Get(r, "username").(string)
	return name
}

// isSelf reports whether target is the account the caller is signed in as.
// Usernames are stored COLLATE NOCASE, so the comparison is case-insensitive
// to match — otherwise "Alice" could delete "alice", which is the same row.
func isSelf(r *http.Request, target *store.User) bool {
	name := sessionUsername(r)
	return name != "" && strings.EqualFold(name, target.Username)
}

// requireStore fetches the process-wide store, writing a 500 when startup has
// not finished.
func requireStore(w http.ResponseWriter) *store.Store {
	st := store.Default()
	if st == nil {
		utils.ResponseErrorStatus(w, store.ErrNoStore, http.StatusInternalServerError)
		return nil
	}
	return st
}

// loadTarget resolves the {id} path value to a user, writing 400 for a
// malformed id and 404 for one that names nobody.
func loadTarget(w http.ResponseWriter, r *http.Request, st *store.Store) *store.User {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		utils.ResponseErrorStatus(w, errors.New("invalid user id"), http.StatusBadRequest)
		return nil
	}

	user, err := st.GetUserByID(r.Context(), id)
	if err != nil {
		utils.ResponseError(w, fmt.Errorf("cannot load user %d: %w", id, err))
		return nil
	}
	if user == nil {
		utils.ResponseErrorStatus(w, store.ErrUserNotFound, http.StatusNotFound)
		return nil
	}
	return user
}

// List returns every account. Hashes cannot appear here: User.PasswordHash is
// `json:"-"`, which is the single guarantee, and the tests check the raw body.
func (c *AdminUsers) List(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	st := requireStore(w)
	if st == nil {
		return
	}

	users, err := st.ListUsers(r.Context())
	if err != nil {
		utils.ResponseError(w, fmt.Errorf("cannot list users: %w", err))
		return
	}

	utils.ResponseSuccess(w, users)
}

// Create adds an account. The password arrives in the request body, so nothing
// in this handler — including the JSON decoder's error — may be echoed back or
// logged.
func (c *AdminUsers) Create(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	st := requireStore(w)
	if st == nil {
		return
	}

	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		// The decoder error can quote the request body, which is exactly where
		// the password is.
		utils.ResponseErrorStatus(w, errors.New("invalid request body"), http.StatusBadRequest)
		return
	}

	// Validate the role before the store does, so an unknown value is a plain
	// 400 rather than something the caller has to infer from a wrapped error.
	if err := store.ValidateRole(body.Role); err != nil {
		utils.ResponseErrorStatus(w, err, http.StatusBadRequest)
		return
	}

	user, err := st.CreateUser(r.Context(), body.Username, body.Password, body.Role)
	switch {
	case errors.Is(err, store.ErrUsernameTaken):
		utils.ResponseErrorStatus(w, err, http.StatusConflict)
		return
	case errors.Is(err, store.ErrWeakPassword),
		errors.Is(err, store.ErrInvalidUsername),
		errors.Is(err, store.ErrInvalidRole):
		utils.ResponseErrorStatus(w, err, http.StatusBadRequest)
		return
	case err != nil:
		utils.ResponseError(w, fmt.Errorf("cannot create user: %w", err))
		return
	}

	log.Printf("admin %q created user %q with role %q", sessionUsername(r), user.Username, user.Role)

	utils.ResponseSuccess(w, user)
}

// Update applies whichever of username / role / disabled the body carries.
// The fields are pointers so that "absent" is distinguishable from "empty":
// {"username": ""} is a rejected rename, {} is a no-op.
func (c *AdminUsers) Update(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	st := requireStore(w)
	if st == nil {
		return
	}

	target := loadTarget(w, r, st)
	if target == nil {
		return
	}

	var body struct {
		Username *string `json:"username"`
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.ResponseErrorStatus(w, errors.New("invalid request body"), http.StatusBadRequest)
		return
	}

	// Reject an unknown role before any of the guards run, so a typo is a 400
	// and not a confusing 409.
	if body.Role != nil {
		if err := store.ValidateRole(*body.Role); err != nil {
			utils.ResponseErrorStatus(w, err, http.StatusBadRequest)
			return
		}
	}

	// Work out what this request would actually take away, then check the
	// lockout rules once, before mutating anything.
	demoting := body.Role != nil && *body.Role == store.RoleViewer && target.Role == store.RoleAdmin
	disabling := body.Disabled != nil && *body.Disabled && !target.Disabled

	if err := ensureNotLastAdmin(r, st, target, demoting || disabling); err != nil {
		writeGuardError(w, err)
		return
	}

	// Apply in a fixed order. Each store call is its own statement, so a
	// failure part-way leaves the earlier changes applied; the alternative is a
	// transaction spanning the whole handler, which this store (a single
	// connection) would serialise anyway. Report the error and stop.
	if body.Username != nil {
		if err := st.Rename(r.Context(), target.ID, *body.Username); err != nil {
			writeUpdateError(w, "rename user", err)
			return
		}
	}
	if body.Role != nil {
		if err := st.SetRole(r.Context(), target.ID, *body.Role); err != nil {
			writeUpdateError(w, "change role", err)
			return
		}
	}
	if body.Disabled != nil {
		if err := st.SetDisabled(r.Context(), target.ID, *body.Disabled); err != nil {
			writeUpdateError(w, "change status", err)
			return
		}
	}

	updated, err := st.GetUserByID(r.Context(), target.ID)
	if err != nil {
		utils.ResponseError(w, fmt.Errorf("cannot reload user %d: %w", target.ID, err))
		return
	}
	if updated == nil {
		utils.ResponseErrorStatus(w, store.ErrUserNotFound, http.StatusNotFound)
		return
	}

	log.Printf("admin %q updated user %q", sessionUsername(r), updated.Username)

	utils.ResponseSuccess(w, updated)
}

// writeUpdateError maps a store error from Update onto a status code.
func writeUpdateError(w http.ResponseWriter, what string, err error) {
	switch {
	case errors.Is(err, store.ErrUserNotFound):
		utils.ResponseErrorStatus(w, err, http.StatusNotFound)
	case errors.Is(err, store.ErrUsernameTaken):
		utils.ResponseErrorStatus(w, err, http.StatusConflict)
	case errors.Is(err, store.ErrInvalidUsername), errors.Is(err, store.ErrInvalidRole):
		utils.ResponseErrorStatus(w, err, http.StatusBadRequest)
	default:
		utils.ResponseError(w, fmt.Errorf("cannot %s: %w", what, err))
	}
}

// Delete removes an account permanently, subject to the lockout guards.
func (c *AdminUsers) Delete(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	st := requireStore(w)
	if st == nil {
		return
	}

	target := loadTarget(w, r, st)
	if target == nil {
		return
	}

	// A delete always removes the target, so the "would this take an admin
	// away" question is unconditionally yes.
	if err := ensureNotLastAdmin(r, st, target, true); err != nil {
		writeGuardError(w, err)
		return
	}

	if err := st.DeleteUser(r.Context(), target.ID); err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			utils.ResponseErrorStatus(w, err, http.StatusNotFound)
			return
		}
		utils.ResponseError(w, fmt.Errorf("cannot delete user: %w", err))
		return
	}

	log.Printf("admin %q deleted user %q", sessionUsername(r), target.Username)

	utils.ResponseSuccess(w, true)
}

// ResetPassword sets another user's password without knowing the old one —
// the administrator's escape hatch for a locked-out colleague.
//
// It deliberately does not renew or invalidate the target's existing sessions:
// that is a separate concern (session storage is in-memory today), and the
// account can always be disabled if the sessions must die now.
//
// Nothing here may return or log the password.
func (c *AdminUsers) ResetPassword(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	st := requireStore(w)
	if st == nil {
		return
	}

	target := loadTarget(w, r, st)
	if target == nil {
		return
	}

	var body struct {
		NewPassword string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		// Never echoed: the decoder error can quote the body, i.e. the password.
		utils.ResponseErrorStatus(w, errors.New("invalid request body"), http.StatusBadRequest)
		return
	}

	if err := st.SetPassword(r.Context(), target.ID, body.NewPassword); err != nil {
		switch {
		case errors.Is(err, store.ErrWeakPassword):
			utils.ResponseErrorStatus(w, err, http.StatusBadRequest)
		case errors.Is(err, store.ErrUserNotFound):
			utils.ResponseErrorStatus(w, err, http.StatusNotFound)
		default:
			utils.ResponseError(w, fmt.Errorf("cannot reset password: %w", err))
		}
		return
	}

	// The log line names the accounts, never the credential.
	log.Printf("admin %q reset the password of user %q", sessionUsername(r), target.Username)

	utils.ResponseSuccess(w, true)
}

// lockoutError marks a refusal produced by the lockout rules themselves, as
// opposed to a failure to evaluate them. The first is a 409 the operator can
// act on; the second is a 500. Both stop the write — see writeGuardError.
type lockoutError struct{ msg string }

func (e *lockoutError) Error() string { return e.msg }

// Lockout guard messages. They are returned to the caller verbatim, so they
// have to explain the refusal on their own.
var (
	errDeleteSelf = &lockoutError{"you cannot delete your own account"}
	errDemoteSelf = &lockoutError{"you cannot demote or disable your own account"}
	errLastAdmin  = &lockoutError{"cannot remove the last administrator: this instance would become unadministrable"}
)

// writeGuardError turns a refusal from ensureNotLastAdmin into a response.
// Anything that is not a lockoutError means the guard could not be evaluated,
// which fails closed as a 500 rather than being reported as a rule violation.
func writeGuardError(w http.ResponseWriter, err error) {
	var lockout *lockoutError
	if errors.As(err, &lockout) {
		utils.ResponseErrorStatus(w, err, http.StatusConflict)
		return
	}
	utils.ResponseError(w, err)
}

// ensureNotLastAdmin decides whether a change may proceed. It rejects anything
// that would remove the final enabled administrator, which would leave the
// instance permanently unadministrable — the /setup wizard only opens when
// there are zero users, so there is no way back from the UI.
//
// removesAdminAccess says whether the pending change would stop the target
// administering: a delete, a disable, or a demotion to viewer. A rename or a
// password reset does not, and is never blocked here.
//
// The three rules:
//  1. an admin may not delete their own account;
//  2. an admin may not demote or disable their own account, even when other
//     admins exist — a one-click accidental lockout is too easy otherwise;
//  3. nobody may take administration away from the last enabled admin.
//
// The CountEnabledAdmins read happens in the same request as the write that
// follows, so two concurrent requests could in principle each see a count of 2
// and both demote. The window is tiny, the operation is manual and privileged,
// and closing it would mean a transaction spanning the handler; recovery from
// the outcome is a DB edit, the same as it is today. Accepted deliberately.
func ensureNotLastAdmin(r *http.Request, st *store.Store, target *store.User, removesAdminAccess bool) error {
	if !removesAdminAccess {
		return nil
	}

	self := isSelf(r, target)
	if self {
		if r.Method == http.MethodDelete {
			return errDeleteSelf
		}
		return errDemoteSelf
	}

	// Only an enabled admin can be the last one; disabled admins and viewers
	// are not counted by CountEnabledAdmins either, so removing them is safe.
	if target.Role != store.RoleAdmin || target.Disabled {
		return nil
	}

	n, err := countEnabledAdmins(r.Context(), st)
	if err != nil {
		return err
	}
	if n <= 1 {
		return errLastAdmin
	}
	return nil
}

// countEnabledAdmins wraps the store call so a database failure surfaces as a
// refusal rather than as a silent "go ahead". Failing closed matters here: an
// unreadable count must never be read as "there are plenty of admins".
func countEnabledAdmins(ctx context.Context, st *store.Store) (int, error) {
	n, err := st.CountEnabledAdmins(ctx)
	if err != nil {
		return 0, fmt.Errorf("cannot verify that another administrator remains: %w", err)
	}
	return n, nil
}

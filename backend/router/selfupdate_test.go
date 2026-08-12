package router

import (
	"crypto/ed25519"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/d7eeem/garage-webui-ng/store"
	"github.com/d7eeem/garage-webui-ng/utils"
)

// genTestKeypair returns a fresh ed25519 keypair for a single test. Never a
// committed key — the whole point is that these tests exercise verification
// against arbitrary keys, not the real release key.
func genTestKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	return pub, priv
}

func TestVerifyChecksumsSignature(t *testing.T) {
	pub, priv := genTestKeypair(t)
	pubHex := hex.EncodeToString(pub)
	checksums := []byte("deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef  garage-webui-ng-linux-amd64\n")
	sig := ed25519.Sign(priv, checksums)
	sigHex := []byte(hex.EncodeToString(sig))

	t.Run("valid signature verifies", func(t *testing.T) {
		if err := verifyChecksumsSignature(pubHex, checksums, sigHex); err != nil {
			t.Errorf("verifyChecksumsSignature() = %v, want nil", err)
		}
	})

	t.Run("empty public key is a hard failure — the fail-closed guard", func(t *testing.T) {
		if err := verifyChecksumsSignature("", checksums, sigHex); err == nil {
			t.Error("verifyChecksumsSignature(pubHex=\"\") = nil, want error — an unconfigured key must never be treated as permissive")
		}
	})

	t.Run("tampered checksums body fails", func(t *testing.T) {
		tampered := append([]byte(nil), checksums...)
		tampered[0] ^= 0xFF
		if err := verifyChecksumsSignature(pubHex, tampered, sigHex); err == nil {
			t.Error("verifyChecksumsSignature() with tampered checksums = nil, want error")
		}
	})

	t.Run("tampered signature fails", func(t *testing.T) {
		sigCopy := append([]byte(nil), sigHex...)
		// Flip the first hex character into a different, still-valid hex digit.
		if sigCopy[0] == '0' {
			sigCopy[0] = 'f'
		} else {
			sigCopy[0] = '0'
		}
		if err := verifyChecksumsSignature(pubHex, checksums, sigCopy); err == nil {
			t.Error("verifyChecksumsSignature() with tampered signature = nil, want error")
		}
	})

	t.Run("signature from a different key fails", func(t *testing.T) {
		otherPub, _ := genTestKeypair(t)
		if err := verifyChecksumsSignature(hex.EncodeToString(otherPub), checksums, sigHex); err == nil {
			t.Error("verifyChecksumsSignature() with wrong key = nil, want error")
		}
	})

	t.Run("non-hex public key errors, does not panic", func(t *testing.T) {
		if err := verifyChecksumsSignature("not-hex-zz", checksums, sigHex); err == nil {
			t.Error("verifyChecksumsSignature() with non-hex key = nil, want error")
		}
	})

	t.Run("wrong-length public key errors, does not panic", func(t *testing.T) {
		if err := verifyChecksumsSignature(hex.EncodeToString([]byte("too short")), checksums, sigHex); err == nil {
			t.Error("verifyChecksumsSignature() with wrong-length key = nil, want error")
		}
	})

	t.Run("non-hex signature errors, does not panic", func(t *testing.T) {
		if err := verifyChecksumsSignature(pubHex, checksums, []byte("not-hex-zz")); err == nil {
			t.Error("verifyChecksumsSignature() with non-hex signature = nil, want error")
		}
	})

	t.Run("wrong-length signature errors, does not panic", func(t *testing.T) {
		if err := verifyChecksumsSignature(pubHex, checksums, []byte(hex.EncodeToString([]byte("too short")))); err == nil {
			t.Error("verifyChecksumsSignature() with wrong-length signature = nil, want error")
		}
	})

	t.Run("trailing newline on the signature file still verifies", func(t *testing.T) {
		withNewline := append(append([]byte(nil), sigHex...), '\n')
		if err := verifyChecksumsSignature(pubHex, checksums, withNewline); err != nil {
			t.Errorf("verifyChecksumsSignature() with trailing newline = %v, want nil", err)
		}
	})
}

func TestChecksumFor(t *testing.T) {
	t.Run("finds the hash for an exact name", func(t *testing.T) {
		body := []byte("aaaa111111111111111111111111111111111111111111111111111111  garage-webui-ng-linux-amd64\nbbbb222222222222222222222222222222222222222222222222222222  garage-webui-ng-linux-arm64\n")
		got, err := checksumFor(body, "garage-webui-ng-linux-amd64")
		if err != nil {
			t.Fatalf("checksumFor() error = %v", err)
		}
		if got != "aaaa111111111111111111111111111111111111111111111111111111" {
			t.Errorf("checksumFor() = %q, want the amd64 hash", got)
		}
	})

	t.Run("does not match on a prefix", func(t *testing.T) {
		body := []byte("cccc333333333333333333333333333333333333333333333333333333  garage-webui-ng-linux-amd64-evil\n")
		if _, err := checksumFor(body, "garage-webui-ng-linux-amd64"); err == nil {
			t.Error("checksumFor() matched a prefix, want error — \"…-amd64-evil\" must not satisfy a request for \"…-amd64\"")
		}
	})

	t.Run("handles the double-space separator", func(t *testing.T) {
		body := []byte("dddd444444444444444444444444444444444444444444444444444444  garage-webui-ng-linux-amd64\n")
		if _, err := checksumFor(body, "garage-webui-ng-linux-amd64"); err != nil {
			t.Errorf("checksumFor() with double-space separator = %v, want nil", err)
		}
	})

	t.Run("handles the space-asterisk separator", func(t *testing.T) {
		body := []byte("eeee555555555555555555555555555555555555555555555555555555 *garage-webui-ng-linux-amd64\n")
		if _, err := checksumFor(body, "garage-webui-ng-linux-amd64"); err != nil {
			t.Errorf("checksumFor() with space-asterisk separator = %v, want nil", err)
		}
	})

	t.Run("ignores blank lines", func(t *testing.T) {
		body := []byte("\n\nffff666666666666666666666666666666666666666666666666666666  garage-webui-ng-linux-amd64\n\n")
		if _, err := checksumFor(body, "garage-webui-ng-linux-amd64"); err != nil {
			t.Errorf("checksumFor() with blank lines = %v, want nil", err)
		}
	})

	t.Run("missing name errors", func(t *testing.T) {
		body := []byte("aaaa111111111111111111111111111111111111111111111111111111  garage-webui-ng-linux-arm64\n")
		if _, err := checksumFor(body, "garage-webui-ng-linux-amd64"); err == nil {
			t.Error("checksumFor() for a missing name = nil, want error")
		}
	})

	t.Run("duplicate entries for one name errors", func(t *testing.T) {
		body := []byte(
			"aaaa111111111111111111111111111111111111111111111111111111  garage-webui-ng-linux-amd64\n" +
				"bbbb222222222222222222222222222222222222222222222222222222  garage-webui-ng-linux-amd64\n",
		)
		if _, err := checksumFor(body, "garage-webui-ng-linux-amd64"); err == nil {
			t.Error("checksumFor() with duplicate entries = nil, want error — a tampered manifest, not a tie-break")
		}
	})
}

// updateApplySession is the identity a test request carries when exercising
// POST /update/apply. A nil session means an anonymous caller.
type updateApplySession struct {
	username string
	role     string
}

// updateApplyHandler serves POST /update/apply through the scs session
// middleware with the given identity already in the session, mirroring
// adminUsersHandler in admin_users_test.go. It deliberately leaves out
// AuthMiddleware and CSRF so these tests exercise requireAdmin — the
// in-handler backstop — directly.
func updateApplyHandler(t *testing.T, sess *updateApplySession) http.Handler {
	t.Helper()
	sessMgr := utils.InitSessionManager() // also sets the package-global utils.Session

	u := &Update{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /update/apply", u.Apply)

	return sessMgr.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sess != nil {
			utils.Session.Set(r, "authenticated", true)
			utils.Session.Set(r, "username", sess.username)
			utils.Session.Set(r, "role", sess.role)
		}
		mux.ServeHTTP(w, r)
	}))
}

// callApply issues one POST /update/apply request against h.
func callApply(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/update/apply", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// TestApplyRefuses covers the refusals that must happen before any outbound
// network call. "Refuses" here means refuses before touching the network,
// not after — both cases point releasesURL at a server that fails the test
// if it is ever hit.
func TestApplyRefuses(t *testing.T) {
	failIfHit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected outbound request: %s %s — Apply must refuse before making any network call", r.Method, r.URL.String())
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failIfHit.Close()

	originalURL := releasesURL
	releasesURL = failIfHit.URL
	t.Cleanup(func() { releasesURL = originalURL })

	t.Run("non-admin gets 403 and no outbound request", func(t *testing.T) {
		originalKey := ReleasePublicKey
		ReleasePublicKey = "aabbccdd" // any non-empty value; must never be reached
		t.Cleanup(func() { ReleasePublicKey = originalKey })

		utils.InitCacheManager()
		h := updateApplyHandler(t, &updateApplySession{username: "bob", role: store.RoleViewer})
		w := callApply(t, h, `{"restart":false}`)

		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d; body=%s", w.Code, http.StatusForbidden, w.Body.String())
		}
	})

	t.Run("admin with no release signing key gets a 4xx naming the missing key, and no outbound request", func(t *testing.T) {
		originalKey := ReleasePublicKey
		ReleasePublicKey = ""
		t.Cleanup(func() { ReleasePublicKey = originalKey })

		utils.InitCacheManager()
		h := updateApplyHandler(t, &updateApplySession{username: "alice", role: store.RoleAdmin})
		w := callApply(t, h, `{"restart":false}`)

		if w.Code < 400 || w.Code >= 500 {
			t.Errorf("status = %d, want 4xx; body=%s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "signing key") {
			t.Errorf("body = %q, want it to mention the missing signing key", w.Body.String())
		}
	})
}

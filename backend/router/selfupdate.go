package router

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/d7eeem/garage-webui-ng/utils"
)

// This file is the only code path in the product that writes an executable
// to disk from a network source, in a process holding the Garage admin
// token. Read the security model in plan 050 before touching it.
//
// The controls, in the order they must run:
//  1. Signature verification is mandatory and fails closed — an empty
//     ReleasePublicKey refuses, always. There is no override.
//  2. SHA256SUMS is attacker-controlled until its ed25519 signature verifies;
//     nothing in it is trusted before that.
//  3. The downloaded binary's SHA-256 must match the exact asset name.
//  4. Admin-only, via the same requireAdmin check /admin/* uses.
//  5. Bounded downloads, a client timeout, and a single-flight guard.
//  6. Nothing downloaded here is ever executed by this process.

// selfUpdateClientTimeout bounds every outbound request this file makes.
const selfUpdateClientTimeout = 30 * time.Second

// selfUpdateMaxBinarySize is roughly 4x the current release binary size — an
// upper bound, not an estimate of the real size.
const selfUpdateMaxBinarySize = 60 * 1024 * 1024

// selfUpdateMaxTextSize bounds SHA256SUMS and SHA256SUMS.sig, both of which
// are a handful of lines.
const selfUpdateMaxTextSize = 1 * 1024 * 1024

// updateInFlight serialises update attempts: two concurrent downloads racing
// to rename onto the same path is how a half-written binary gets installed.
var updateInFlight atomic.Bool

// assetDownloadURL builds a GitHub release asset URL. A package-level var,
// like releasesURL, so tests can point it at an httptest server without a
// real network call.
var assetDownloadURL = func(tag, name string) string {
	return "https://github.com/d7eeem/garage-webui-ng/releases/download/" + tag + "/" + name
}

// assetName is the release asset this build should install.
func assetName() string {
	return "garage-webui-ng-linux-" + runtime.GOARCH
}

// verifyChecksumsSignature reports whether sigHex (hex ed25519 signature,
// possibly with surrounding whitespace as published) is a valid signature
// over the exact bytes of checksums, under pubHex.
//
// Order is the security property: SHA256SUMS is attacker-controlled until
// this returns nil, so no hash inside it may be used before that. An empty
// pubHex is a hard failure — this build cannot verify anything, and "cannot
// verify" must never degrade into "install anyway".
func verifyChecksumsSignature(pubHex string, checksums, sigHex []byte) error {
	if pubHex == "" {
		return errors.New("no release signing key configured for this build")
	}

	pubBytes, err := hex.DecodeString(strings.TrimSpace(pubHex))
	if err != nil {
		return fmt.Errorf("decode public key: %w", err)
	}
	if len(pubBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("public key has wrong length: got %d bytes, want %d", len(pubBytes), ed25519.PublicKeySize)
	}

	sigBytes, err := hex.DecodeString(strings.TrimSpace(string(sigHex)))
	if err != nil {
		return fmt.Errorf("decode signature hex: %w", err)
	}
	if len(sigBytes) != ed25519.SignatureSize {
		return fmt.Errorf("signature has wrong length: got %d bytes, want %d", len(sigBytes), ed25519.SignatureSize)
	}

	if !ed25519.Verify(ed25519.PublicKey(pubBytes), checksums, sigBytes) {
		return errors.New("signature verification failed")
	}
	return nil
}

// checksumFor extracts the expected SHA-256 for exactly `name` from a
// verified SHA256SUMS body (`<64-hex>  <name>` or `<64-hex> *<name>` per
// line, the two spacings `sha256sum` uses for text/binary mode). Matching is
// on the full name; a prefix or substring match would let "…-amd64-evil"
// satisfy "…-amd64". A duplicate entry for the same name is rejected outright
// — two lines naming one file is a tampered manifest, not a tie-break.
//
// Callers MUST have already verified the signature over checksums via
// verifyChecksumsSignature — this function trusts its input completely.
func checksumFor(checksums []byte, name string) (string, error) {
	found := ""
	seen := false

	scanner := bufio.NewScanner(bytes.NewReader(checksums))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		hash, rest, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		// Tolerate both "  name" (a lone leading space remains after the Cut
		// above split on the first of the two) and " *name" (binary mode).
		rest = strings.TrimPrefix(rest, " ")
		rest = strings.TrimPrefix(rest, "*")
		if rest != name {
			continue
		}

		if seen {
			return "", fmt.Errorf("duplicate checksum entry for %q", name)
		}
		seen = true
		found = strings.ToLower(hash)
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan checksums: %w", err)
	}
	if !seen {
		return "", fmt.Errorf("no checksum entry found for %q", name)
	}
	return found, nil
}

// applyRequest is the POST /update/apply request body.
type applyRequest struct {
	// Restart opts into the process signalling its own graceful shutdown
	// after a successful swap. Default (false, including an absent field) is
	// the safe choice: the binary is staged and the running process keeps
	// serving the old version until the operator restarts it manually. See
	// the package doc comment above and plan 050 §0.
	Restart bool `json:"restart"`
}

// applyResponse reports what actually happened.
type applyResponse struct {
	Version         string `json:"version"`
	RestartRequired bool   `json:"restartRequired"`
	Restarting      bool   `json:"restarting"`
	BackupPath      string `json:"backupPath,omitempty"`
}

// Apply downloads, verifies and stages the latest release binary. See the
// package doc comment for the security model this implements.
func (u *Update) Apply(w http.ResponseWriter, r *http.Request) {
	// 1. Admin only, independent of any outer routing.
	if !requireAdmin(w, r) {
		return
	}

	// 2. Fail closed: no key configured, no download, ever. This check must
	// stand before any network call below.
	if ReleasePublicKey == "" {
		utils.ResponseErrorStatus(w, errors.New("this build has no release signing key configured; in-browser update is unavailable"), http.StatusBadRequest)
		return
	}

	// 3. Only a writable-by-us executable can be staged in place.
	if detectDeployment() != deploymentBinary {
		utils.ResponseErrorStatus(w, errors.New("the running executable is not writable by this process; this deployment must be updated from outside (e.g. pull a new container image or use your package manager)"), http.StatusBadRequest)
		return
	}

	// 4. Single-flight: never let two updates race onto the same path.
	if !updateInFlight.CompareAndSwap(false, true) {
		utils.ResponseErrorStatus(w, errors.New("an update is already in progress"), http.StatusConflict)
		return
	}
	defer updateInFlight.Store(false)

	// 5. Decode the body. A malformed body is a flat message — never echo it
	// back, matching the convention for every other body-accepting handler
	// in this package.
	var body applyRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.ResponseErrorStatus(w, errors.New("invalid request body"), http.StatusBadRequest)
		return
	}

	// 6. Reuse the existing update-check machinery; do not duplicate it.
	release, err := fetchLatestRelease(r.Context())
	if err != nil {
		utils.ResponseError(w, fmt.Errorf("check for the latest release: %w", err))
		return
	}
	if !isNewer(AppVersion, release.TagName) {
		utils.ResponseErrorStatus(w, errors.New("already up to date"), http.StatusBadRequest)
		return
	}

	// 7. Download the three assets. Everything here is held in memory, never
	// written to disk, until verification (steps 8-9) passes.
	client := &http.Client{Timeout: selfUpdateClientTimeout}
	name := assetName()

	binBytes, err := downloadAsset(r.Context(), client, release.TagName, name, selfUpdateMaxBinarySize)
	if err != nil {
		utils.ResponseError(w, fmt.Errorf("download %s: %w", name, err))
		return
	}
	checksums, err := downloadAsset(r.Context(), client, release.TagName, "SHA256SUMS", selfUpdateMaxTextSize)
	if err != nil {
		utils.ResponseError(w, fmt.Errorf("download SHA256SUMS: %w", err))
		return
	}
	sig, err := downloadAsset(r.Context(), client, release.TagName, "SHA256SUMS.sig", selfUpdateMaxTextSize)
	if err != nil {
		utils.ResponseError(w, fmt.Errorf("download SHA256SUMS.sig: %w", err))
		return
	}

	// 8. THE gate. Nothing below may run if this fails, and nothing above
	// this point has touched the filesystem.
	if err := verifyChecksumsSignature(ReleasePublicKey, checksums, sig); err != nil {
		log.Printf("self-update: refusing %s — checksum manifest signature invalid: %v", release.TagName, err)
		utils.ResponseErrorStatus(w, errors.New("release signature verification failed; refusing to install"), http.StatusBadGateway)
		return
	}

	// 9. Only now is a hash from SHA256SUMS trusted.
	wantHash, err := checksumFor(checksums, name)
	if err != nil {
		utils.ResponseError(w, fmt.Errorf("locate checksum for %s: %w", name, err))
		return
	}
	gotHash := sha256.Sum256(binBytes)
	if hex.EncodeToString(gotHash[:]) != wantHash {
		log.Printf("self-update: refusing %s — downloaded binary does not match its published checksum", release.TagName)
		utils.ResponseErrorStatus(w, errors.New("downloaded binary does not match its published checksum; refusing to install"), http.StatusBadGateway)
		return
	}

	// 10. Only now does anything touch the filesystem.
	exe, err := os.Executable()
	if err != nil {
		utils.ResponseError(w, fmt.Errorf("locate the running executable: %w", err))
		return
	}

	backupPath, err := stageBinary(exe, binBytes)
	if err != nil {
		utils.ResponseError(w, fmt.Errorf("stage new binary: %w", err))
		return
	}

	log.Printf("self-update: staged %s (was %s), backup at %s", release.TagName, AppVersion, backupPath)

	// 11. Respond with what actually happened.
	resp := applyResponse{
		Version:         release.TagName,
		RestartRequired: true,
		Restarting:      body.Restart,
		BackupPath:      backupPath,
	}
	utils.ResponseSuccess(w, resp)

	// 12. Only after the response above has been handed to the ResponseWriter
	// do we ever consider ending this process — and even then, only on
	// explicit opt-in, and only via the existing graceful-shutdown signal
	// path (never os.Exit, which would drop in-flight requests).
	if body.Restart {
		go signalSelfForRestart()
	}
}

// downloadAsset fetches a single release asset, bounding its size with
// io.LimitReader so a compromised or misbehaving upstream can never exhaust
// memory.
func downloadAsset(ctx context.Context, client *http.Client, tag, name string, maxSize int64) ([]byte, error) {
	url := assetDownloadURL(tag, name)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	// Read one byte past the limit so an over-size asset is detected rather
	// than silently truncated.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("exceeds the %d byte limit for this asset", maxSize)
	}
	return data, nil
}

// stageBinary writes newBinary to disk and atomically installs it at exe.
//
//   - The temp file is created with os.CreateTemp in the SAME directory as
//     exe — a different filesystem would make the final rename non-atomic —
//     so a partial write during a crash is never left at a predictable name.
//   - The current binary is best-effort copied to exe+".bak" first: a
//     failure to back up is logged but does not abort an otherwise-verified
//     update, per plan 050. The returned backupPath is empty when the backup
//     could not be made.
//   - os.Rename(tmp, exe) is the atomic swap.
//
// Every failure path removes the temp file — a leftover temp file is a
// stale-binary hazard, not just clutter.
func stageBinary(exe string, newBinary []byte) (backupPath string, err error) {
	dir := filepath.Dir(exe)

	tmp, err := os.CreateTemp(dir, ".garage-webui-ng-update-*")
	if err != nil {
		return "", fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			os.Remove(tmpPath)
		}
	}()

	if chmodErr := tmp.Chmod(0o755); chmodErr != nil {
		tmp.Close()
		return "", fmt.Errorf("chmod temp file: %w", chmodErr)
	}
	if _, writeErr := tmp.Write(newBinary); writeErr != nil {
		tmp.Close()
		return "", fmt.Errorf("write temp file: %w", writeErr)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		return "", fmt.Errorf("close temp file: %w", closeErr)
	}

	backupPath = exe + ".bak"
	if backupErr := copyFile(exe, backupPath); backupErr != nil {
		log.Printf("self-update: could not back up %s to %s (continuing anyway): %v", exe, backupPath, backupErr)
		backupPath = ""
	}

	if renameErr := os.Rename(tmpPath, exe); renameErr != nil {
		return backupPath, fmt.Errorf("rename staged binary onto %s: %w", exe, renameErr)
	}
	// Successfully renamed away — nothing left at tmpPath to clean up.
	removeTemp = false
	return backupPath, nil
}

// copyFile copies src to dst, creating or truncating dst with an executable
// mode.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// signalSelfForRestart sends this process SIGTERM, which main.go's existing
// graceful-shutdown path (signal.Notify + srv.Shutdown) already handles. A
// short delay gives the HTTP response written just before this was called a
// chance to reach the client first. Never os.Exit: that would skip the
// drain of in-flight requests entirely.
func signalSelfForRestart() {
	time.Sleep(200 * time.Millisecond)

	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		log.Printf("self-update: cannot locate own process to signal a restart: %v", err)
		return
	}
	if err := p.Signal(syscall.SIGTERM); err != nil {
		log.Printf("self-update: cannot signal own process for restart: %v", err)
	}
}

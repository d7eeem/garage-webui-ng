package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// writeTempFile writes content to a new file under t.TempDir() and returns
// its path.
func writeTempFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// signWithKey is a small test helper that runs the sign subcommand with the
// given hex private key set on the named env var, and returns the error (if
// any) from runSign.
func signWithKey(t *testing.T, keyEnv, keyHex, in, out string) error {
	t.Helper()
	t.Setenv(keyEnv, keyHex)
	return runSign([]string{"-key-env", keyEnv, "-in", in, "-out", out})
}

func TestSignVerifyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	in := writeTempFile(t, dir, "SHA256SUMS", []byte("deadbeef  garage-webui-ng-linux-amd64\n"))
	out := filepath.Join(dir, "SHA256SUMS.sig")

	if err := signWithKey(t, "TEST_ROUNDTRIP_KEY", hex.EncodeToString(priv), in, out); err != nil {
		t.Fatalf("sign: %v", err)
	}

	if err := runVerify([]string{"-pub", hex.EncodeToString(pub), "-in", in, "-sig", out}); err != nil {
		t.Fatalf("verify should succeed for an untampered file, got: %v", err)
	}
}

func TestVerifyFailsOnTamperedPayload(t *testing.T) {
	dir := t.TempDir()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	in := writeTempFile(t, dir, "SHA256SUMS", []byte("deadbeef  garage-webui-ng-linux-amd64\n"))
	out := filepath.Join(dir, "SHA256SUMS.sig")

	if err := signWithKey(t, "TEST_TAMPER_PAYLOAD_KEY", hex.EncodeToString(priv), in, out); err != nil {
		t.Fatalf("sign: %v", err)
	}

	// Flip one byte of the signed file after signing.
	tampered, err := os.ReadFile(in)
	if err != nil {
		t.Fatalf("read signed file: %v", err)
	}
	tampered[0] ^= 0xFF
	if err := os.WriteFile(in, tampered, 0o644); err != nil {
		t.Fatalf("rewrite signed file: %v", err)
	}

	if err := runVerify([]string{"-pub", hex.EncodeToString(pub), "-in", in, "-sig", out}); err == nil {
		t.Fatal("verify should fail for a tampered payload, got nil error")
	}
}

func TestVerifyFailsOnTamperedSignature(t *testing.T) {
	dir := t.TempDir()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	in := writeTempFile(t, dir, "SHA256SUMS", []byte("deadbeef  garage-webui-ng-linux-amd64\n"))
	out := filepath.Join(dir, "SHA256SUMS.sig")

	if err := signWithKey(t, "TEST_TAMPER_SIG_KEY", hex.EncodeToString(priv), in, out); err != nil {
		t.Fatalf("sign: %v", err)
	}

	sigHex, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read signature: %v", err)
	}
	trimmed := strings.TrimSpace(string(sigHex))
	// Flip the first hex character; swap between '0' and 'f' so the result
	// is always a different, still-valid hex digit.
	mutatedChar := byte('0')
	if trimmed[0] == '0' {
		mutatedChar = 'f'
	}
	newSig := string(mutatedChar) + trimmed[1:]
	if err := os.WriteFile(out, []byte(newSig+"\n"), 0o644); err != nil {
		t.Fatalf("rewrite signature: %v", err)
	}

	if err := runVerify([]string{"-pub", hex.EncodeToString(pub), "-in", in, "-sig", out}); err == nil {
		t.Fatal("verify should fail for a tampered signature, got nil error")
	}
}

func TestVerifyFailsWithWrongKey(t *testing.T) {
	dir := t.TempDir()
	_, privA, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key A: %v", err)
	}
	pubB, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key B: %v", err)
	}

	in := writeTempFile(t, dir, "SHA256SUMS", []byte("deadbeef  garage-webui-ng-linux-amd64\n"))
	out := filepath.Join(dir, "SHA256SUMS.sig")

	if err := signWithKey(t, "TEST_WRONG_KEY", hex.EncodeToString(privA), in, out); err != nil {
		t.Fatalf("sign: %v", err)
	}

	if err := runVerify([]string{"-pub", hex.EncodeToString(pubB), "-in", in, "-sig", out}); err == nil {
		t.Fatal("verify should fail when the public key does not match the signer, got nil error")
	}
}

func TestSignRejectsMalformedKey(t *testing.T) {
	dir := t.TempDir()
	in := writeTempFile(t, dir, "SHA256SUMS", []byte("deadbeef  garage-webui-ng-linux-amd64\n"))
	out := filepath.Join(dir, "SHA256SUMS.sig")

	cases := []struct {
		name string
		key  string
	}{
		{"non-hex", "not-hex-at-all-zz"},
		{"wrong-length", hex.EncodeToString([]byte("too short"))},
		{"empty", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			envName := "TEST_MALFORMED_KEY_" + strings.ReplaceAll(tc.name, "-", "_")
			t.Setenv(envName, tc.key)
			err := runSign([]string{"-key-env", envName, "-in", in, "-out", out})
			if err == nil {
				t.Fatalf("expected error for malformed key %q, got nil", tc.name)
			}
		})
	}
}

func TestSignRejectsMissingEnvVar(t *testing.T) {
	dir := t.TempDir()
	in := writeTempFile(t, dir, "SHA256SUMS", []byte("deadbeef  garage-webui-ng-linux-amd64\n"))
	out := filepath.Join(dir, "SHA256SUMS.sig")

	envName := "TEST_UNSET_KEY_VAR_DOES_NOT_EXIST"
	os.Unsetenv(envName)

	err := runSign([]string{"-key-env", envName, "-in", in, "-out", out})
	if err == nil {
		t.Fatal("expected error when the named env var is unset, got nil")
	}
}

func TestVerifyRejectsEmptySignatureFile(t *testing.T) {
	dir := t.TempDir()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	in := writeTempFile(t, dir, "SHA256SUMS", []byte("deadbeef  garage-webui-ng-linux-amd64\n"))
	out := writeTempFile(t, dir, "SHA256SUMS.sig", []byte(""))

	if err := runVerify([]string{"-pub", hex.EncodeToString(pub), "-in", in, "-sig", out}); err == nil {
		t.Fatal("verify should fail for an empty signature file, got nil error")
	}
}

func TestVerifyRejectsMalformedPublicKey(t *testing.T) {
	dir := t.TempDir()
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	in := writeTempFile(t, dir, "SHA256SUMS", []byte("deadbeef  garage-webui-ng-linux-amd64\n"))
	out := filepath.Join(dir, "SHA256SUMS.sig")
	if err := signWithKey(t, "TEST_VERIFY_MALFORMED_PUB_KEY", hex.EncodeToString(priv), in, out); err != nil {
		t.Fatalf("sign: %v", err)
	}

	cases := []struct {
		name string
		pub  string
	}{
		{"non-hex", "zz-not-hex"},
		{"wrong-length", hex.EncodeToString([]byte("short"))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := runVerify([]string{"-pub", tc.pub, "-in", in, "-sig", out})
			if err == nil {
				t.Fatalf("expected error for malformed public key %q, got nil", tc.name)
			}
		})
	}
}

// TestKeygenProducesValidKeypair runs the keygen subcommand and checks that
// the hex values it prints decode to correctly-sized ed25519 keys that
// actually work together. Output is captured into in-memory buffers only —
// never printed, logged, or written to a file — and the key material is
// discarded at the end of the test, per the "ephemeral, in-memory only"
// rule for keys generated during testing.
func TestKeygenProducesValidKeypair(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := runKeygen(nil, &stdout, &stderr); err != nil {
		t.Fatalf("keygen: %v", err)
	}

	privHex := strings.TrimSpace(stdout.String())
	// stderr now has more than one line (the labelled public key, then a
	// reminder note) — the public key is on the first line.
	stderrFirstLine, _, _ := strings.Cut(stderr.String(), "\n")
	pubHex := extractHexAfterColon(t, stderrFirstLine)

	privBytes, err := hex.DecodeString(privHex)
	if err != nil {
		t.Fatalf("private key output is not valid hex: %v", err)
	}
	if len(privBytes) != ed25519.PrivateKeySize {
		t.Fatalf("private key has wrong length: got %d bytes, want %d", len(privBytes), ed25519.PrivateKeySize)
	}

	pubBytes, err := hex.DecodeString(pubHex)
	if err != nil {
		t.Fatalf("public key output is not valid hex: %v", err)
	}
	if len(pubBytes) != ed25519.PublicKeySize {
		t.Fatalf("public key has wrong length: got %d bytes, want %d", len(pubBytes), ed25519.PublicKeySize)
	}

	// The two halves must actually be a matching pair.
	msg := []byte("keygen self-check")
	sig := ed25519.Sign(ed25519.PrivateKey(privBytes), msg)
	if !ed25519.Verify(ed25519.PublicKey(pubBytes), msg, sig) {
		t.Fatal("printed private/public keys are not a matching pair")
	}
}

// stdoutHexPattern matches exactly 64 bytes of lowercase hex (the encoded
// size of an ed25519 private key) and nothing else.
var stdoutHexPattern = regexp.MustCompile(`^[0-9a-f]{128}$`)

// TestKeygenStdoutIsBareHex is the regression guard for the bug that broke
// the v3.7.0 release: a label on stdout got piped into the
// RELEASE_SIGNING_KEY secret and corrupted it. stdout must carry nothing
// but the bare private key hex, on one line.
func TestKeygenStdoutIsBareHex(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := runKeygen(nil, &stdout, &stderr); err != nil {
		t.Fatalf("keygen: %v", err)
	}

	trimmed := strings.TrimSpace(stdout.String())
	if !stdoutHexPattern.MatchString(trimmed) {
		t.Fatalf("stdout must be exactly 128 lowercase hex characters and nothing else, got %q", stdout.String())
	}
}

// TestKeygenStdoutRoundTripsThroughDecode proves the pipe-to-secret path
// works end to end without a human in the middle: whatever keygen printed
// to stdout, fed straight into decodePrivateKey (as a secret store would
// hand it back), must decode without error.
func TestKeygenStdoutRoundTripsThroughDecode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := runKeygen(nil, &stdout, &stderr); err != nil {
		t.Fatalf("keygen: %v", err)
	}

	stdoutValue := strings.TrimSpace(stdout.String())
	if _, err := decodePrivateKey(stdoutValue); err != nil {
		t.Fatalf("decodePrivateKey(stdout output) = %v, want no error", err)
	}
}

// TestKeygenStderrCarriesPublicKeyOnly checks that stderr contains the
// public key but never the private key hex — a copy-paste slip that also
// printed the private half to stderr would otherwise pass unnoticed.
func TestKeygenStderrCarriesPublicKeyOnly(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := runKeygen(nil, &stdout, &stderr); err != nil {
		t.Fatalf("keygen: %v", err)
	}

	privHex := strings.TrimSpace(stdout.String())
	stderrText := stderr.String()

	if !strings.Contains(stderrText, "PUBLIC KEY") {
		t.Fatalf("expected stderr to contain a labelled public key line, got %q", stderrText)
	}
	if strings.Contains(stderrText, privHex) {
		t.Fatalf("stderr must never contain the private key hex, but it did: stderr=%q priv=%q", stderrText, privHex)
	}
}

// TestDecodeKeyToleratesSurroundingWhitespace checks that a secret store or
// shell pipeline appending a newline (or a copy-paste adding a leading
// space) doesn't break decoding — trimming removes a whole class of
// unreproducible support problem.
func TestDecodeKeyToleratesSurroundingWhitespace(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	privHex := hex.EncodeToString(priv)
	pubHex := hex.EncodeToString(pub)

	variants := []struct {
		name string
		wrap func(s string) string
	}{
		{"trailing-newline", func(s string) string { return s + "\n" }},
		{"leading-space", func(s string) string { return " " + s }},
		{"leading-and-trailing", func(s string) string { return " " + s + "\n" }},
	}

	for _, v := range variants {
		t.Run("private/"+v.name, func(t *testing.T) {
			if _, err := decodePrivateKey(v.wrap(privHex)); err != nil {
				t.Fatalf("decodePrivateKey(%q) = %v, want no error", v.wrap(privHex), err)
			}
		})
		t.Run("public/"+v.name, func(t *testing.T) {
			if _, err := decodePublicKey(v.wrap(pubHex)); err != nil {
				t.Fatalf("decodePublicKey(%q) = %v, want no error", v.wrap(pubHex), err)
			}
		})
	}
}

// TestDecodeKeyRejectsInteriorWhitespace ensures TrimSpace only strips
// leading/trailing whitespace — a space inserted in the middle of a key is
// a corrupt key, not a formatting artefact, and must still be rejected.
func TestDecodeKeyRejectsInteriorWhitespace(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	privHex := hex.EncodeToString(priv)
	pubHex := hex.EncodeToString(pub)

	mid := len(privHex) / 2
	brokenPriv := privHex[:mid] + " " + privHex[mid:]
	if _, err := decodePrivateKey(brokenPriv); err == nil {
		t.Fatal("decodePrivateKey with interior whitespace should fail, got nil error")
	}

	mid = len(pubHex) / 2
	brokenPub := pubHex[:mid] + " " + pubHex[mid:]
	if _, err := decodePublicKey(brokenPub); err == nil {
		t.Fatal("decodePublicKey with interior whitespace should fail, got nil error")
	}
}

// extractHexAfterColon returns the token after the last ": " in s, trimmed
// of surrounding whitespace/newlines — matching the "LABEL: <hex>" line
// format the subcommands print.
func extractHexAfterColon(t *testing.T, s string) string {
	t.Helper()
	idx := strings.LastIndex(s, ": ")
	if idx == -1 {
		t.Fatalf("expected a labelled ': <hex>' line, got %q", s)
	}
	return strings.TrimSpace(s[idx+2:])
}

// TestSignRejectsKeyPassedAsFlag guards the design decision that the
// private key can ONLY be supplied via the environment. There is
// deliberately no -key (or similar) flag on the sign subcommand; passing
// one must be rejected as an unrecognised flag, not silently accepted as an
// alternate way to supply the key.
func TestSignRejectsKeyPassedAsFlag(t *testing.T) {
	dir := t.TempDir()
	in := writeTempFile(t, dir, "SHA256SUMS", []byte("deadbeef  garage-webui-ng-linux-amd64\n"))
	out := filepath.Join(dir, "SHA256SUMS.sig")

	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	err = runSign([]string{"-key", hex.EncodeToString(priv), "-in", in, "-out", out})
	if err == nil {
		t.Fatal("sign must reject a private key supplied via a flag; got nil error")
	}
}

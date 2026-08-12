// Command relsign generates, signs, and verifies the SHA256SUMS file that
// accompanies each Garage WebUI-NG release. It is deliberately minimal:
// three subcommands, stdlib crypto only, no third-party dependencies.
//
// The private key never touches a command-line flag: CI process tables and
// job logs are not a safe place for it, so `sign` reads it from a named
// environment variable instead.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		usage(stderr)
		return 2
	}

	var err error
	switch args[0] {
	case "keygen":
		err = runKeygen(args[1:], stdout, stderr)
	case "sign":
		err = runSign(args[1:])
	case "verify":
		err = runVerify(args[1:])
	case "-h", "-help", "--help", "help":
		usage(stderr)
		return 0
	default:
		fmt.Fprintf(stderr, "relsign: unknown subcommand %q\n", args[0])
		usage(stderr)
		return 2
	}

	if err != nil {
		fmt.Fprintln(stderr, "relsign:", err)
		return 1
	}
	return 0
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `usage:
  relsign keygen
      Generates an ed25519 keypair. Prints the private key (hex) to stdout
      and the public key (hex) to stderr.

  relsign sign -key-env <ENV_VAR> -in <file> -out <sig-file>
      Signs the exact bytes of <file> using the hex private key read from
      the named environment variable, and writes the hex signature.

  relsign verify -pub <hex> -in <file> -sig <sig-file>
      Verifies <sig-file> against <file> for the given hex public key.
      Exits non-zero on any failure.`)
}

// runKeygen generates a fresh ed25519 keypair and prints it: the private
// half to stdout, the public half to stderr, each on one clearly labelled
// line. Splitting the streams means a caller can capture just the public
// key (e.g. `relsign keygen 2>pub.txt >/dev/null` is wrong on purpose — see
// README) without an accidental copy-paste of the private half.
func runKeygen(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("keygen takes no arguments")
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate keypair: %w", err)
	}

	fmt.Fprintln(stdout, "PRIVATE KEY (hex, secret — store as the RELEASE_SIGNING_KEY GitHub secret, never commit):", hex.EncodeToString(priv))
	fmt.Fprintln(stderr, "PUBLIC KEY (hex, safe to share — paste into backend/release_key.go):", hex.EncodeToString(pub))
	return nil
}

// runSign reads the private key ONLY from the environment variable named by
// -key-env. There is deliberately no flag that accepts the key value itself:
// a flag value is visible in the process table and often ends up in CI logs.
func runSign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	keyEnv := fs.String("key-env", "", "name of the environment variable holding the hex-encoded ed25519 private key")
	in := fs.String("in", "", "path to the file to sign")
	out := fs.String("out", "", "path to write the hex-encoded signature")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("sign takes no positional arguments")
	}
	if *keyEnv == "" || *in == "" || *out == "" {
		return fmt.Errorf("sign requires -key-env, -in and -out")
	}

	keyHex, ok := os.LookupEnv(*keyEnv)
	if !ok || keyHex == "" {
		return fmt.Errorf("environment variable %s is not set", *keyEnv)
	}

	priv, err := decodePrivateKey(keyHex)
	if err != nil {
		// Deliberately do not include the raw value, its length, or any
		// prefix of it in this error.
		return fmt.Errorf("decode private key from $%s: %w", *keyEnv, err)
	}

	payload, err := os.ReadFile(*in)
	if err != nil {
		return fmt.Errorf("read %s: %w", *in, err)
	}

	// Sign the file's exact bytes — no re-serialisation, so verification can
	// never disagree with signing about formatting.
	sig := ed25519.Sign(priv, payload)

	if err := os.WriteFile(*out, []byte(hex.EncodeToString(sig)+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", *out, err)
	}
	return nil
}

func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	pubHex := fs.String("pub", "", "hex-encoded ed25519 public key")
	in := fs.String("in", "", "path to the signed file")
	sigPath := fs.String("sig", "", "path to the hex-encoded signature file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("verify takes no positional arguments")
	}
	if *pubHex == "" || *in == "" || *sigPath == "" {
		return fmt.Errorf("verify requires -pub, -in and -sig")
	}

	pub, err := decodePublicKey(*pubHex)
	if err != nil {
		return fmt.Errorf("decode public key: %w", err)
	}

	payload, err := os.ReadFile(*in)
	if err != nil {
		return fmt.Errorf("read %s: %w", *in, err)
	}

	sigRaw, err := os.ReadFile(*sigPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", *sigPath, err)
	}

	sig, err := hex.DecodeString(strings.TrimSpace(string(sigRaw)))
	if err != nil {
		return fmt.Errorf("decode signature hex: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("signature has wrong length: got %d bytes, want %d", len(sig), ed25519.SignatureSize)
	}

	if !ed25519.Verify(pub, payload, sig) {
		return fmt.Errorf("signature verification failed")
	}
	return nil
}

func decodePrivateKey(s string) (ed25519.PrivateKey, error) {
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("invalid hex encoding: %w", err)
	}
	if len(b) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("wrong length: got %d bytes, want %d", len(b), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(b), nil
}

func decodePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("invalid hex encoding: %w", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("wrong length: got %d bytes, want %d", len(b), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(b), nil
}

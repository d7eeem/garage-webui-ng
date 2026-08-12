package main

// releasePublicKey is the hex-encoded ed25519 public key that release
// artifacts are signed with. The matching private key lives only in the
// repository's RELEASE_SIGNING_KEY GitHub secret and must never appear here or
// anywhere else in this tree.
//
// EMPTY MEANS UNCONFIGURED, AND UNCONFIGURED MUST FAIL CLOSED: any feature that
// installs a downloaded artifact has to refuse to run when this is empty,
// rather than falling back to "no verification". See plan 050.
//
// To configure: run `go run ./cmd/relsign keygen`, put the private key in the
// GitHub secret, and paste the public key here.
var releasePublicKey = ""

// ReleasePublicKey returns the configured release-signing public key, or "" if
// this build has none.
func ReleasePublicKey() string { return releasePublicKey }

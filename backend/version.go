package main

// version is the release identity of this binary, injected at build time with
//
//	-ldflags "-X main.version=v3.3.0"
//
// It defaults to "dev" so a plain `go build` / `make build` is always honest
// about being an untagged local build rather than claiming a release number.
//
// The GIT TAG is the source of truth, not package.json. The tag is what
// produces the GitHub release and the ghcr.io semver image tags, and it is what
// an operator quotes in a bug report. package.json is metadata on a
// "private": true package that is never published; it must FOLLOW the tag, and
// a CI guard fails a release when the two disagree.
var version = "dev"

// Version returns the running build's version, never empty.
func Version() string {
	if version == "" {
		return "dev"
	}
	return version
}

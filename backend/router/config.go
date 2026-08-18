package router

import (
	"github.com/d7eeem/garage-webui-ng/schema"
	"github.com/d7eeem/garage-webui-ng/utils"
	"net/http"
)

type Config struct{}

// AppVersion is the running build's version string (see backend/version.go).
// Package router cannot import package main, so main() pushes the value in
// here once at startup, the same way it installs the utils.Garage / store
// singletons. Defaults to "dev" so a handler running before that assignment
// (there is none today, but nothing enforces it) still returns something
// honest rather than an empty string.
var AppVersion = "dev"

// ReleasePublicKey is the hex ed25519 public key releases are signed with,
// injected from main at startup because release_key.go is package main and Go
// forbids importing it. Empty means this build cannot verify a release, and
// every self-update path must refuse to run — see selfupdate.go.
var ReleasePublicKey string

func (c *Config) GetAll(w http.ResponseWriter, r *http.Request) {
	resp := schema.NewConfigResponse(utils.Garage.Config)
	resp.Sharing = utils.Garage.IsSharingEnabled()
	resp.S3Web.PublicURL = utils.Garage.GetWebPublicURL()
	resp.Version = AppVersion
	utils.ResponseSuccess(w, resp)
}

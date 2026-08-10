package schema

type Config struct {
	RPCBindAddr   string `json:"rpc_bind_addr" toml:"rpc_bind_addr"`
	RPCPublicAddr string `json:"rpc_public_addr" toml:"rpc_public_addr"`
	RPCSecret     string `json:"rpc_secret" toml:"rpc_secret"`
	Admin         Admin  `json:"admin" toml:"admin"`
	S3API         S3API  `json:"s3_api" toml:"s3_api"`
	S3Web         S3Web  `json:"s3_web" toml:"s3_web"`
}

type Admin struct {
	AdminToken   string `json:"admin_token" toml:"admin_token"`
	APIBindAddr  string `json:"api_bind_addr" toml:"api_bind_addr"`
	MetricsToken string `json:"metrics_token" toml:"metrics_token"`
}

type S3API struct {
	APIBindAddr string `json:"api_bind_addr" toml:"api_bind_addr"`
	RootDomain  string `json:"root_domain" toml:"root_domain"`
	S3Region    string `json:"s3_region" toml:"s3_region"`
}

type S3Web struct {
	BindAddr   string `json:"bind_addr" toml:"bind_addr"`
	Index      string `json:"index" toml:"index"`
	RootDomain string `json:"root_domain" toml:"root_domain"`
}

// ConfigResponse is the subset of the Garage configuration that is safe to
// return to the browser. Secret-bearing fields (rpc_secret, admin_token,
// metrics_token) are deliberately absent — the UI never needs them.
type ConfigResponse struct {
	S3API   S3APIResponse `json:"s3_api"`
	S3Web   S3WebResponse `json:"s3_web"`
	Sharing bool          `json:"sharing"`
	// Version is the running build's release identity, injected at build time.
	// Not from garage.toml, so NewConfigResponse leaves it empty — the handler
	// fills it, exactly as it fills Sharing.
	Version string `json:"version"`
}

type S3APIResponse struct {
	RootDomain string `json:"root_domain"`
	S3Region   string `json:"s3_region"`
}

type S3WebResponse struct {
	BindAddr   string `json:"bind_addr"`
	RootDomain string `json:"root_domain"`
	Index      string `json:"index"`
	// PublicURL is env-derived (S3_WEB_PUBLIC_URL), not read from garage.toml,
	// so NewConfigResponse leaves it empty — the handler in
	// backend/router/config.go fills it, the same way it fills Sharing.
	PublicURL string `json:"public_url"`
}

// NewConfigResponse projects a parsed Config onto the browser-safe subset.
func NewConfigResponse(c Config) ConfigResponse {
	return ConfigResponse{
		S3API: S3APIResponse{
			RootDomain: c.S3API.RootDomain,
			S3Region:   c.S3API.S3Region,
		},
		S3Web: S3WebResponse{
			BindAddr:   c.S3Web.BindAddr,
			RootDomain: c.S3Web.RootDomain,
			Index:      c.S3Web.Index,
		},
	}
}

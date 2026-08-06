package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNewConfigResponseOmitsSecrets(t *testing.T) {
	cfg := Config{
		RPCBindAddr:   "0.0.0.0:3901",
		RPCPublicAddr: "192.0.2.1:3901",
		RPCSecret:     "dummy-rpc-secret",
		Admin: Admin{
			AdminToken:   "dummy-admin-token",
			APIBindAddr:  "0.0.0.0:3903",
			MetricsToken: "dummy-metrics-token",
		},
		S3API: S3API{
			APIBindAddr: "0.0.0.0:3900",
			RootDomain:  "s3.example.com",
			S3Region:    "garage",
		},
		S3Web: S3Web{
			BindAddr:   "0.0.0.0:3902",
			Index:      "index.html",
			RootDomain: "web.example.com",
		},
	}

	resp := NewConfigResponse(cfg)

	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal ConfigResponse: %v", err)
	}
	body := string(b)

	dummyValues := []string{"dummy-rpc-secret", "dummy-admin-token", "dummy-metrics-token"}
	for _, v := range dummyValues {
		if strings.Contains(body, v) {
			t.Errorf("marshalled ConfigResponse contains secret value %q: %s", v, body)
		}
	}

	secretKeys := []string{"rpc_secret", "admin_token", "metrics_token"}
	for _, k := range secretKeys {
		if strings.Contains(body, k) {
			t.Errorf("marshalled ConfigResponse contains secret key %q: %s", k, body)
		}
	}
}

func TestNewConfigResponseKeepsWebFields(t *testing.T) {
	cfg := Config{
		S3API: S3API{
			RootDomain: "s3.example.com",
			S3Region:   "garage",
		},
		S3Web: S3Web{
			BindAddr:   "0.0.0.0:3902",
			Index:      "index.html",
			RootDomain: "web.example.com",
		},
	}

	resp := NewConfigResponse(cfg)

	if resp.S3Web.BindAddr != "0.0.0.0:3902" {
		t.Errorf("expected s3_web.bind_addr %q, got %q", "0.0.0.0:3902", resp.S3Web.BindAddr)
	}
	if resp.S3Web.RootDomain != "web.example.com" {
		t.Errorf("expected s3_web.root_domain %q, got %q", "web.example.com", resp.S3Web.RootDomain)
	}
	if resp.S3API.RootDomain != "s3.example.com" {
		t.Errorf("expected s3_api.root_domain %q, got %q", "s3.example.com", resp.S3API.RootDomain)
	}
}

func TestNewConfigResponseLeavesPublicURLEmpty(t *testing.T) {
	cfg := Config{
		S3Web: S3Web{
			BindAddr:   "0.0.0.0:3902",
			Index:      "index.html",
			RootDomain: "web.example.com",
		},
	}

	resp := NewConfigResponse(cfg)

	// PublicURL is env-derived, not a garage.toml field, so NewConfigResponse
	// must never populate it — the handler in backend/router/config.go is
	// responsible for that.
	if resp.S3Web.PublicURL != "" {
		t.Errorf("expected s3_web.public_url to be empty, got %q", resp.S3Web.PublicURL)
	}
}

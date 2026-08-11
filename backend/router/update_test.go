package router

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/d7eeem/garage-webui-ng/utils"
)

func TestIsNewer(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{name: "equal", current: "3.3.0", latest: "3.3.0", want: false},
		{name: "patch bump", current: "3.3.0", latest: "3.4.0", want: true},
		{name: "current ahead", current: "3.4.0", latest: "3.3.0", want: false},
		{name: "v prefix on latest", current: "3.3.0", latest: "v3.4.0", want: true},
		{name: "v prefix on current", current: "v3.3.0", latest: "3.4.0", want: true},
		{name: "v prefix on both, equal", current: "v3.3.0", latest: "v3.3.0", want: false},
		{name: "missing patch component", current: "3.3", latest: "3.3.0", want: false},
		{name: "current is dev", current: "dev", latest: "3.3.0", want: false},
		{name: "latest is dev", current: "3.3.0", latest: "dev", want: false},
		{name: "latest has pre-release suffix", current: "3.3.0", latest: "3.3.0-rc1", want: false},
		{name: "empty current", current: "", latest: "3.3.0", want: false},
		{name: "empty latest", current: "3.3.0", latest: "", want: false},
		// The string-compare trap: "3.10.0" < "3.9.0" lexicographically, but
		// numerically 3.10.0 is newer. This is why isNewer must not just
		// string-compare.
		{name: "double-digit minor beats single-digit minor", current: "3.9.0", latest: "3.10.0", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNewer(tt.current, tt.latest); got != tt.want {
				t.Errorf("isNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}

func TestUpdateGetDisabled(t *testing.T) {
	utils.InitCacheManager()
	t.Setenv("UPDATE_CHECK_ENABLED", "")

	originalAppVersion := AppVersion
	AppVersion = "3.3.0"
	t.Cleanup(func() { AppVersion = originalAppVersion })

	req := httptest.NewRequest(http.MethodGet, "/update-check", nil)
	w := httptest.NewRecorder()

	(&Update{}).Get(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var got UpdateCheck
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if got.Enabled {
		t.Errorf("expected enabled=false, got true")
	}
	if got.Current != "3.3.0" {
		t.Errorf("current = %q, want %q", got.Current, "3.3.0")
	}
}

func TestUpdateGetEnabledNewerTag(t *testing.T) {
	utils.InitCacheManager()
	t.Setenv("UPDATE_CHECK_ENABLED", "true")

	originalAppVersion := AppVersion
	AppVersion = "3.3.0"
	t.Cleanup(func() { AppVersion = originalAppVersion })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(githubRelease{
			TagName: "v3.4.0",
			HTMLURL: "https://github.com/d7eeem/garage-webui-ng/releases/tag/v3.4.0",
		})
	}))
	defer server.Close()

	originalURL := releasesURL
	releasesURL = server.URL
	t.Cleanup(func() { releasesURL = originalURL })

	req := httptest.NewRequest(http.MethodGet, "/update-check", nil)
	w := httptest.NewRecorder()

	(&Update{}).Get(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var got UpdateCheck
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !got.Enabled {
		t.Errorf("expected enabled=true, got false")
	}
	if !got.UpdateAvailable {
		t.Errorf("expected updateAvailable=true, got false")
	}
	if got.Latest != "v3.4.0" {
		t.Errorf("latest = %q, want %q", got.Latest, "v3.4.0")
	}
	if got.CheckFailed {
		t.Errorf("expected checkFailed=false, got true")
	}
}

func TestUpdateGetEnabledUpstreamFailure(t *testing.T) {
	utils.InitCacheManager()
	t.Setenv("UPDATE_CHECK_ENABLED", "true")

	originalAppVersion := AppVersion
	AppVersion = "3.3.0"
	t.Cleanup(func() { AppVersion = originalAppVersion })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("upstream is down, sensitive details here"))
	}))
	defer server.Close()

	originalURL := releasesURL
	releasesURL = server.URL
	t.Cleanup(func() { releasesURL = originalURL })

	req := httptest.NewRequest(http.MethodGet, "/update-check", nil)
	w := httptest.NewRecorder()

	(&Update{}).Get(w, req)

	// A failed upstream must never surface as a 5xx to the caller — it
	// degrades quietly.
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if strings.Contains(body, "upstream is down") {
		t.Errorf("response leaked upstream body: %s", body)
	}

	var got UpdateCheck
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !got.CheckFailed {
		t.Errorf("expected checkFailed=true, got false")
	}
	if !got.Enabled {
		t.Errorf("expected enabled=true, got false")
	}
}

func TestClassifyOpenResult(t *testing.T) {
	tests := []struct {
		name    string
		execErr error
		openErr error
		want    DeploymentKind
	}{
		{name: "executable path unknown", execErr: errors.New("boom"), openErr: nil, want: deploymentUnknown},
		{name: "writable executable", execErr: nil, openErr: nil, want: deploymentBinary},
		{name: "permission denied", execErr: nil, openErr: fs.ErrPermission, want: deploymentManaged},
		{name: "other open error fails safe to managed", execErr: nil, openErr: errors.New("read-only file system"), want: deploymentManaged},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyOpenResult(tt.execErr, tt.openErr); got != tt.want {
				t.Errorf("classifyOpenResult(%v, %v) = %v, want %v", tt.execErr, tt.openErr, got, tt.want)
			}
		})
	}
}

func TestUpdateCommandFor(t *testing.T) {
	tests := []struct {
		name string
		kind DeploymentKind
		want func(cmd string) bool
	}{
		// managed covers both a container AND a hardened systemd service
		// (root-owned binary, e.g. ProtectSystem=strict) — two setups with no
		// shared update command. Guessing one (e.g. "docker compose pull")
		// would mis-advise the other, so this must be exactly "".
		{name: "managed", kind: deploymentManaged, want: func(cmd string) bool {
			return cmd == ""
		}},
		{name: "binary", kind: deploymentBinary, want: func(cmd string) bool {
			return strings.Contains(cmd, "systemctl") && strings.Contains(cmd, "install")
		}},
		{name: "unknown", kind: deploymentUnknown, want: func(cmd string) bool {
			return cmd == ""
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := updateCommandFor(tt.kind)
			if !tt.want(got) {
				t.Errorf("updateCommandFor(%v) = %q, did not satisfy expectation", tt.kind, got)
			}
			if strings.Contains(got, "\n") {
				t.Errorf("updateCommandFor(%v) = %q, contains a newline; the UI copies it as one line", tt.kind, got)
			}
		})
	}

	// Regression guard for this amendment: managed must never carry a
	// container-specific example, even if the equality check above is
	// weakened by a future edit.
	t.Run("managed never suggests docker", func(t *testing.T) {
		got := updateCommandFor(deploymentManaged)
		if strings.Contains(got, "docker") {
			t.Errorf("updateCommandFor(managed) = %q, contains %q; managed also covers hardened systemd services with no docker compose file, so this command would mis-advise them", got, "docker")
		}
	})
}

func TestDeploymentFieldsAreSetWhenDisabled(t *testing.T) {
	utils.InitCacheManager()
	t.Setenv("UPDATE_CHECK_ENABLED", "")

	originalAppVersion := AppVersion
	AppVersion = "3.3.0"
	t.Cleanup(func() { AppVersion = originalAppVersion })

	req := httptest.NewRequest(http.MethodGet, "/update-check", nil)
	w := httptest.NewRecorder()

	(&Update{}).Get(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var got UpdateCheck
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if got.Enabled {
		t.Errorf("expected enabled=false, got true")
	}
	if got.Deployment == "" {
		t.Errorf("expected a non-empty deployment kind even when update checks are disabled, got %q", got.Deployment)
	}
}

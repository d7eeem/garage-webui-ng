package router

import (
	"encoding/json"
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

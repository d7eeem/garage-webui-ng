package router

import (
	"net/http"
	"testing"
	"time"
)

func TestLoginLimiterAllowsUpToLimit(t *testing.T) {
	limiter := newLoginLimiter(3, time.Minute)
	now := time.Now()

	for i := 0; i < 3; i++ {
		if !limiter.allow("client", now) {
			t.Fatalf("attempt %d: allow() = false, want true", i+1)
		}
	}

	if limiter.allow("client", now) {
		t.Fatal("4th attempt: allow() = true, want false")
	}
}

func TestLoginLimiterWindowExpires(t *testing.T) {
	limiter := newLoginLimiter(3, time.Minute)
	now := time.Now()

	for i := 0; i < 3; i++ {
		if !limiter.allow("client", now) {
			t.Fatalf("attempt %d: allow() = false, want true", i+1)
		}
	}

	if limiter.allow("client", now) {
		t.Fatal("4th attempt within window: allow() = true, want false")
	}

	later := now.Add(2 * time.Minute)
	if !limiter.allow("client", later) {
		t.Fatal("attempt after window expiry: allow() = false, want true")
	}
}

func TestLoginLimiterIsPerKey(t *testing.T) {
	limiter := newLoginLimiter(3, time.Minute)
	now := time.Now()

	for i := 0; i < 3; i++ {
		if !limiter.allow("a", now) {
			t.Fatalf("key a, attempt %d: allow() = false, want true", i+1)
		}
	}

	if limiter.allow("a", now) {
		t.Fatal("key a, 4th attempt: allow() = true, want false")
	}

	if !limiter.allow("b", now) {
		t.Fatal("key b, 1st attempt: allow() = false, want true (should be unaffected by key a)")
	}
}

func TestClientIPStripsPort(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		want       string
	}{
		{
			name:       "valid host:port",
			remoteAddr: "192.0.2.1:54321",
			want:       "192.0.2.1",
		},
		{
			name:       "malformed remote addr",
			remoteAddr: "not-a-valid-address",
			want:       "not-a-valid-address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Request{RemoteAddr: tt.remoteAddr}
			if got := clientIP(r); got != tt.want {
				t.Errorf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

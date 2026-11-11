package netguard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "2a00:1450::1": true,
		"127.0.0.1": false, "10.1.2.3": false, "192.168.0.1": false, "172.16.5.5": false, "169.254.169.254": false,
		"100.64.0.1": false, "::1": false, "fd00::1": false, "fe80::1": false, "0.0.0.0": false, "::ffff:127.0.0.1": false,
	} {
		if got := Public(netip.MustParseAddr(addr)); got != want {
			t.Errorf("Public(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestCheckURL(t *testing.T) {
	g, err := New([]string{"photon.internal", "10.20.0.0/16"}, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for raw, ok := range map[string]bool{
		"https://8.8.8.8/api":            true,
		"http://photon.internal:2322":    true,
		"http://10.20.1.5":               true,
		"http://127.0.0.1:8080":          false,
		"http://169.254.169.254/meta":    false,
		"http://[::1]/":                  false,
		"file:///etc/passwd":             false,
		"gopher://8.8.8.8":               false,
		"https://user:pw@8.8.8.8/":       false,
		"http://localhost:5432":          false,
		"http://10.21.0.1":               false,
		"https://does-not-exist.invalid": false,
	} {
		err := g.CheckURL(ctx, raw)
		if (err == nil) != ok {
			t.Errorf("CheckURL(%s) = %v, want ok=%v", raw, err, ok)
		}
		if err != nil && !errors.Is(err, ErrBlocked) {
			t.Errorf("CheckURL(%s): error is not ErrBlocked: %v", raw, err)
		}
	}
	if _, err := New([]string{"10.0.0.0/99"}, false); err == nil {
		t.Error("bad CIDR accepted")
	}
}

func TestClientRefusesInternalAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()

	g, _ := New(nil, false)
	_, err := g.Client(2 * time.Second).Get(srv.URL)
	if err == nil || !strings.Contains(err.Error(), "address not allowed") {
		t.Fatalf("request to 127.0.0.1 should be refused, got %v", err)
	}
	allowed, _ := New([]string{"127.0.0.0/8"}, false)
	res, err := allowed.Client(2 * time.Second).Get(srv.URL)
	if err != nil {
		t.Fatalf("allowlisted CIDR: %v", err)
	}
	_ = res.Body.Close()
}

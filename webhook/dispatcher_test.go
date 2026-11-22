package webhook

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"pusher-clone/config"
	"pusher-clone/core"
)

func newTestDispatcher() *Dispatcher {
	return NewDispatcher(config.NewManagerFromConfig(&config.Config{}), core.NoopDebugNotifier{})
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	d := newTestDispatcher()
	req, err := http.NewRequest(http.MethodPost, "https://example.com/hook", nil)
	if err != nil {
		t.Fatalf("Failed to build request: %v", err)
	}
	if err := d.client.CheckRedirect(req, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("Expected redirects to be disabled, got %v", err)
	}
}

func TestClientRefusesPrivateDestinations(t *testing.T) {
	d := newTestDispatcher()
	transport, ok := d.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Unexpected transport type %T", d.client.Transport)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, addr := range []string{"127.0.0.1:80", "localhost:80", "10.0.0.1:80", "[::1]:80"} {
		conn, err := transport.DialContext(ctx, "tcp", addr)
		if conn != nil {
			conn.Close()
		}
		if err == nil {
			t.Errorf("Expected dial to %s to be refused", addr)
		}
	}
}

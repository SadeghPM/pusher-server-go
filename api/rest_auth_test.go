package api

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"pusher-clone/config"
	"pusher-clone/core"
)

func testApp() *config.AppConfig {
	return &config.AppConfig{
		AppID:     "123",
		AppKey:    "test-key",
		AppSecret: "test-secret",
	}
}

func md5Hex(value string) string {
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}

// signTestRequest builds a request using an independent implementation of the
// documented Pusher signing algorithm, including every non-auth_signature query
// parameter.
func signTestRequest(t *testing.T, app *config.AppConfig, path, body string, timestamp time.Time, extra url.Values) *http.Request {
	t.Helper()

	params := url.Values{}
	params.Set("auth_key", app.AppKey)
	params.Set("auth_timestamp", fmt.Sprintf("%d", timestamp.Unix()))
	params.Set("auth_version", "1.0")

	if body != "" {
		params.Set("body_md5", md5Hex(body))
	}

	for key, values := range extra {
		params[key] = append([]string(nil), values...)
	}

	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, strings.ToLower(key))
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+params.Get(key))
	}

	stringToSign := fmt.Sprintf("%s\n%s\n%s", http.MethodPost, path, strings.Join(parts, "&"))
	mac := hmac.New(sha256.New, []byte(app.AppSecret))
	mac.Write([]byte(stringToSign))
	params.Set("auth_signature", hex.EncodeToString(mac.Sum(nil)))

	request, err := http.NewRequest(
		http.MethodPost,
		path+"?"+params.Encode(),
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}

	return request
}

func TestAuthenticateRequestRejectsTimestampOutsideWindow(t *testing.T) {
	app := testApp()
	body := `{"name":"evt","channel":"ch","data":"{}"}`
	now := time.Unix(1700000000, 0)

	tests := []struct {
		name      string
		timestamp time.Time
	}{
		{"too old", now.Add(-601 * time.Second)},
		{"too far in the future", now.Add(601 * time.Second)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := signTestRequest(t, app, "/apps/123/events", body, tc.timestamp, nil)

			err := authenticateRequestAt(request, []byte(body), app, now)
			if err == nil || !strings.Contains(err.Error(), "Timestamp") {
				t.Fatalf("expected timestamp rejection, got %v", err)
			}
		})
	}
}

func TestAuthenticateRequestAcceptsTimestampAtWindowEdge(t *testing.T) {
	app := testApp()
	body := `{"name":"evt","channel":"ch","data":"{}"}`
	now := time.Unix(1700000000, 0)

	for _, timestamp := range []time.Time{
		now.Add(-600 * time.Second),
		now.Add(600 * time.Second),
	} {
		request := signTestRequest(t, app, "/apps/123/events", body, timestamp, nil)

		if err := authenticateRequestAt(request, []byte(body), app, now); err != nil {
			t.Fatalf("timestamp at the edge of the window was rejected: %v", err)
		}
	}
}

func TestAuthenticateRequestSignsExtraQueryParams(t *testing.T) {
	app := testApp()
	body := `{"name":"evt","channel":"ch","data":"{}"}`
	now := time.Now()
	extra := url.Values{"info": {"user_count"}}

	request := signTestRequest(t, app, "/apps/123/events", body, now, extra)
	if err := authenticateRequestAt(request, []byte(body), app, now); err != nil {
		t.Fatalf("request with an extra signed query parameter was rejected: %v", err)
	}

	// Changing a signed parameter without re-signing must invalidate the request.
	tamperedURL, err := url.Parse(request.URL.String())
	if err != nil {
		t.Fatalf("failed to parse request URL: %v", err)
	}
	query := tamperedURL.Query()
	query.Set("info", "other_value")
	tamperedURL.RawQuery = query.Encode()

	tamperedRequest, err := http.NewRequest(
		http.MethodPost,
		tamperedURL.String(),
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("failed to build tampered request: %v", err)
	}

	if err := authenticateRequestAt(tamperedRequest, []byte(body), app, now); err == nil {
		t.Fatal("request with a modified extra query parameter was accepted")
	}
}

func TestAuthenticateRequestAllowsEmptyBodyWithoutMD5(t *testing.T) {
	app := testApp()
	now := time.Now()

	request := signTestRequest(t, app, "/apps/123/events", "", now, nil)
	if err := authenticateRequestAt(request, nil, app, now); err != nil {
		t.Fatalf("empty-body request without body_md5 was rejected: %v", err)
	}
}

func TestHandleEventsRejectsOversizedData(t *testing.T) {
	cfg := &config.Config{
		Port: "6001",
		Apps: []config.AppConfig{*testApp()},
	}
	globalHub := core.NewGlobalHub(nil, nil)
	api := NewAPI(globalHub, config.NewManagerFromConfig(cfg))

	tests := []struct {
		name       string
		dataLength int
		wantStatus int
	}{
		{"at limit", maxEventDataLength, http.StatusOK},
		{"over limit", maxEventDataLength + 1, http.StatusRequestEntityTooLarge},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(TriggerPayload{
				Name:    "evt",
				Channel: "ch",
				Data:    strings.Repeat("x", tc.dataLength),
			})
			if err != nil {
				t.Fatalf("failed to marshal payload: %v", err)
			}

			request := signedEventsRequest(body, testApp().AppKey, testApp().AppSecret)
			recorder := httptest.NewRecorder()
			api.HandleEvents(recorder, request, testApp().AppID)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
		})
	}
}

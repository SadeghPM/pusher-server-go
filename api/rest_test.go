package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"pusher-clone/config"
	"pusher-clone/core"
)

func TestHandleEventsBadAppID(t *testing.T) {
	cfg := &config.Config{
		Port: "8080",
		Apps: []config.AppConfig{
			{
				AppID:     "123",
				AppKey:    "test-key",
				AppSecret: "test-secret",
			},
		},
	}
	globalHub := core.NewGlobalHub()
	api := NewAPI(globalHub, cfg)

	body := []byte(`{"name":"my-event","channel":"my-channel","data":"{\"message\":\"hello\"}"}`)

	req := httptest.NewRequest("POST", "/apps/wrong-id/events", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	api.HandleEvents(rr, req, "wrong-id")

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}
}

func TestHandleEventsAuth(t *testing.T) {
	cfg := &config.Config{
		Port: "8080",
		Apps: []config.AppConfig{
			{
				AppID:     "123",
				AppKey:    "test-key",
				AppSecret: "test-secret",
			},
		},
	}
	globalHub := core.NewGlobalHub()
	api := NewAPI(globalHub, cfg)

	body := []byte(`{"name":"my-event","channel":"my-channel","data":"{\"message\":\"hello\"}"}`)

	// Create MD5
	hasher := md5.New()
	hasher.Write(body)
	bodyMD5 := hex.EncodeToString(hasher.Sum(nil))

	authKey := "test-key"
	authTimestamp := "1234567890"
	authVersion := "1.0"

	// Create Signature
	queryParams := fmt.Sprintf("auth_key=%s&auth_timestamp=%s&auth_version=%s&body_md5=%s", authKey, authTimestamp, authVersion, bodyMD5)
	stringToSign := fmt.Sprintf("POST\n/apps/123/events\n%s", queryParams)

	mac := hmac.New(sha256.New, []byte("test-secret"))
	mac.Write([]byte(stringToSign))
	authSignature := hex.EncodeToString(mac.Sum(nil))

	url := fmt.Sprintf("/apps/123/events?auth_key=%s&auth_timestamp=%s&auth_version=%s&body_md5=%s&auth_signature=%s",
		authKey, authTimestamp, authVersion, bodyMD5, authSignature)

	req := httptest.NewRequest("POST", url, bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	api.HandleEvents(rr, req, "123")

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}
}

func TestHandleEventsAuthFailure_BadKey(t *testing.T) {
	cfg := &config.Config{
		Port: "8080",
		Apps: []config.AppConfig{
			{
				AppID:     "123",
				AppKey:    "test-key",
				AppSecret: "test-secret",
			},
		},
	}
	globalHub := core.NewGlobalHub()
	api := NewAPI(globalHub, cfg)

	body := []byte(`{"name":"my-event","channel":"my-channel","data":"{\"message\":\"hello\"}"}`)

	// Create MD5
	hasher := md5.New()
	hasher.Write(body)
	bodyMD5 := hex.EncodeToString(hasher.Sum(nil))

	authKey := "wrong-key"
	authTimestamp := "1234567890"
	authVersion := "1.0"

	// Create Signature
	queryParams := fmt.Sprintf("auth_key=%s&auth_timestamp=%s&auth_version=%s&body_md5=%s", authKey, authTimestamp, authVersion, bodyMD5)
	stringToSign := fmt.Sprintf("POST\n/apps/123/events\n%s", queryParams)

	mac := hmac.New(sha256.New, []byte("test-secret"))
	mac.Write([]byte(stringToSign))
	authSignature := hex.EncodeToString(mac.Sum(nil))

	url := fmt.Sprintf("/apps/123/events?auth_key=%s&auth_timestamp=%s&auth_version=%s&body_md5=%s&auth_signature=%s",
		authKey, authTimestamp, authVersion, bodyMD5, authSignature)

	req := httptest.NewRequest("POST", url, bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	api.HandleEvents(rr, req, "123")

	if status := rr.Code; status != http.StatusUnauthorized {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusUnauthorized)
	}
}

func TestHandleEventsAuthFailure_BadMD5(t *testing.T) {
	cfg := &config.Config{
		Port: "8080",
		Apps: []config.AppConfig{
			{
				AppID:     "123",
				AppKey:    "test-key",
				AppSecret: "test-secret",
			},
		},
	}
	globalHub := core.NewGlobalHub()
	api := NewAPI(globalHub, cfg)

	body := []byte(`{"name":"my-event","channel":"my-channel","data":"{\"message\":\"hello\"}"}`)

	// Incorrect MD5
	bodyMD5 := "00000000000000000000000000000000"

	authKey := "test-key"
	authTimestamp := "1234567890"
	authVersion := "1.0"

	// Create Signature
	queryParams := fmt.Sprintf("auth_key=%s&auth_timestamp=%s&auth_version=%s&body_md5=%s", authKey, authTimestamp, authVersion, bodyMD5)
	stringToSign := fmt.Sprintf("POST\n/apps/123/events\n%s", queryParams)

	mac := hmac.New(sha256.New, []byte("test-secret"))
	mac.Write([]byte(stringToSign))
	authSignature := hex.EncodeToString(mac.Sum(nil))

	url := fmt.Sprintf("/apps/123/events?auth_key=%s&auth_timestamp=%s&auth_version=%s&body_md5=%s&auth_signature=%s",
		authKey, authTimestamp, authVersion, bodyMD5, authSignature)

	req := httptest.NewRequest("POST", url, bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	api.HandleEvents(rr, req, "123")

	if status := rr.Code; status != http.StatusUnauthorized {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusUnauthorized)
	}
}

func TestHandleEventsAuthFailure_BadSignature(t *testing.T) {
	cfg := &config.Config{
		Port: "8080",
		Apps: []config.AppConfig{
			{
				AppID:     "123",
				AppKey:    "test-key",
				AppSecret: "test-secret",
			},
		},
	}
	globalHub := core.NewGlobalHub()
	api := NewAPI(globalHub, cfg)

	body := []byte(`{"name":"my-event","channel":"my-channel","data":"{\"message\":\"hello\"}"}`)

	// Create MD5
	hasher := md5.New()
	hasher.Write(body)
	bodyMD5 := hex.EncodeToString(hasher.Sum(nil))

	authKey := "test-key"
	authTimestamp := "1234567890"
	authVersion := "1.0"

	authSignature := "0000000000000000000000000000000000000000000000000000000000000000"

	url := fmt.Sprintf("/apps/123/events?auth_key=%s&auth_timestamp=%s&auth_version=%s&body_md5=%s&auth_signature=%s",
		authKey, authTimestamp, authVersion, bodyMD5, authSignature)

	req := httptest.NewRequest("POST", url, bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	api.HandleEvents(rr, req, "123")

	if status := rr.Code; status != http.StatusUnauthorized {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusUnauthorized)
	}
}

func TestHandleEventsInvalidJSON(t *testing.T) {
	cfg := &config.Config{
		Port: "8080",
		Apps: []config.AppConfig{
			{
				AppID:     "123",
				AppKey:    "test-key",
				AppSecret: "test-secret",
			},
		},
	}
	globalHub := core.NewGlobalHub()
	api := NewAPI(globalHub, cfg)

	body := []byte(`{invalid json`)

	// Create MD5
	hasher := md5.New()
	hasher.Write(body)
	bodyMD5 := hex.EncodeToString(hasher.Sum(nil))

	authKey := "test-key"
	authTimestamp := "1234567890"
	authVersion := "1.0"

	// Create Signature
	queryParams := fmt.Sprintf("auth_key=%s&auth_timestamp=%s&auth_version=%s&body_md5=%s", authKey, authTimestamp, authVersion, bodyMD5)
	stringToSign := fmt.Sprintf("POST\n/apps/123/events\n%s", queryParams)

	mac := hmac.New(sha256.New, []byte("test-secret"))
	mac.Write([]byte(stringToSign))
	authSignature := hex.EncodeToString(mac.Sum(nil))

	url := fmt.Sprintf("/apps/123/events?auth_key=%s&auth_timestamp=%s&auth_version=%s&body_md5=%s&auth_signature=%s",
		authKey, authTimestamp, authVersion, bodyMD5, authSignature)

	req := httptest.NewRequest("POST", url, bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	api.HandleEvents(rr, req, "123")

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}
}

func TestHandleEventsMethodNotAllowed(t *testing.T) {
	cfg := &config.Config{
		Port: "8080",
		Apps: []config.AppConfig{
			{
				AppID:     "123",
				AppKey:    "test-key",
				AppSecret: "test-secret",
			},
		},
	}
	globalHub := core.NewGlobalHub()
	api := NewAPI(globalHub, cfg)

	url := "/apps/123/events"
	req := httptest.NewRequest("GET", url, nil)
	rr := httptest.NewRecorder()

	api.HandleEvents(rr, req, "123")

	if status := rr.Code; status != http.StatusMethodNotAllowed {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusMethodNotAllowed)
	}
}

func TestHandleEventsAppNotFound(t *testing.T) {
	cfg := &config.Config{
		Port: "8080",
		Apps: []config.AppConfig{
			{
				AppID:     "123",
				AppKey:    "test-key",
				AppSecret: "test-secret",
			},
		},
	}
	globalHub := core.NewGlobalHub()
	api := NewAPI(globalHub, cfg)

	url := "/apps/999/events"
	req := httptest.NewRequest("POST", url, bytes.NewBuffer([]byte(`{}`)))
	rr := httptest.NewRecorder()

	api.HandleEvents(rr, req, "999")

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}
}

type errReader int

func (errReader) Read(p []byte) (n int, err error) {
	return 0, errors.New("test error")
}

func TestHandleEventsBadBody(t *testing.T) {
	cfg := &config.Config{
		Port: "8080",
		Apps: []config.AppConfig{
			{
				AppID:     "123",
				AppKey:    "test-key",
				AppSecret: "test-secret",
			},
		},
	}
	globalHub := core.NewGlobalHub()
	api := NewAPI(globalHub, cfg)

	url := "/apps/123/events"
	req := httptest.NewRequest("POST", url, errReader(0))
	rr := httptest.NewRecorder()

	api.HandleEvents(rr, req, "123")

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}
}

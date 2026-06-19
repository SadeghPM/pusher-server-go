package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"pusher-clone/config"
	"pusher-clone/core"
)

func TestHandleEventsAuth(t *testing.T) {
	cfg := &config.Config{
		AppID:     "123",
		AppKey:    "test-key",
		AppSecret: "test-secret",
	}
	hub := core.NewHub()
	api := NewAPI(hub, cfg)

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

	mac := hmac.New(sha256.New, []byte(cfg.AppSecret))
	mac.Write([]byte(stringToSign))
	authSignature := hex.EncodeToString(mac.Sum(nil))

	url := fmt.Sprintf("/apps/123/events?auth_key=%s&auth_timestamp=%s&auth_version=%s&body_md5=%s&auth_signature=%s",
		authKey, authTimestamp, authVersion, bodyMD5, authSignature)

	req := httptest.NewRequest("POST", url, bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	handler := http.HandlerFunc(api.HandleEvents)
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}
}

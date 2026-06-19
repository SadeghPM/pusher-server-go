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

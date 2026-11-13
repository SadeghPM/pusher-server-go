package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"pusher-clone/config"
)

// TestOfficialPusherHTTPSignatureVector verifies the worked example published
// in Pusher's official HTTP API specification:
// https://github.com/pusher/pusher-http-api-reference/blob/master/pusher-http-api.yaml
func TestOfficialPusherHTTPSignatureVector(t *testing.T) {
	const body = `{"name":"foo","channels":["project-3"],"data":"{\"some\":\"data\"}"}`
	const requestURL = "http://api.pusherapp.com/apps/3/events?auth_key=278d425bdf160c739803&auth_signature=da454824c97ba181a32ccc17a72625ba02771f50b50e1e7430e47a1f3f457e6c&auth_timestamp=1353088179&auth_version=1.0&body_md5=ec365a775a4cd0599faeb73354201b6f"

	request, err := http.NewRequest(http.MethodPost, requestURL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}

	app := &config.AppConfig{
		AppID:     "3",
		AppKey:    "278d425bdf160c739803",
		AppSecret: "7ad3773142a6692b25b8",
	}

	if err := authenticateRequestAt(request, []byte(body), app, time.Unix(1353088179, 0)); err != nil {
		t.Fatalf("official Pusher HTTP signature vector was rejected: %v", err)
	}
}

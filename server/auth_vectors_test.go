package server

import "testing"

const officialTestSecret = "7ad3773142a6692b25b8"

// TestOfficialPrivateChannelAuthVector verifies the private-channel signature
// vector published by the official Pusher HTTP Go SDK.
func TestOfficialPrivateChannelAuthVector(t *testing.T) {
	const want = "58df8b0c36d6982b82c3ecf6b4662e34fe8c25bba48f5369f135bf843651c3a4"

	got := generateSignature(officialTestSecret, "1234.1234:private-foobar")
	if got != want {
		t.Fatalf("private channel auth signature = %q, want %q", got, want)
	}
}

// TestOfficialPresenceChannelAuthVector verifies the presence-channel
// signature vector published by the official Pusher HTTP Go SDK.
func TestOfficialPresenceChannelAuthVector(t *testing.T) {
	const channelData = `{"user_id":"10","user_info":{"name":"Mr. Pusher"}}`
	const want = "48dac51d2d7569e1e9c0f48c227d4b26f238fa68e5c0bb04222c966909c4f7c4"

	got := generateSignature(
		officialTestSecret,
		"1234.1234:presence-foobar:"+channelData,
	)
	if got != want {
		t.Fatalf("presence channel auth signature = %q, want %q", got, want)
	}
}

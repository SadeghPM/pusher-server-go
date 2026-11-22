package core

import (
	"regexp"
	"time"
)

const (
	// MaxChannelNameLength matches the official Pusher limit.
	MaxChannelNameLength = 200

	// MaxPresenceMembersPerChannel bounds the size of a presence member list.
	MaxPresenceMembersPerChannel = 100
	// MaxPresenceUserInfoBytes bounds the user_info blob accepted on subscribe.
	MaxPresenceUserInfoBytes = 4096
	// MaxPresenceChannelDataBytes bounds the whole channel_data JSON object.
	MaxPresenceChannelDataBytes = 8192
	// MaxPresenceUserIDLength bounds a presence user_id.
	MaxPresenceUserIDLength = 128

	// MaxChannelsPerConnection prevents a single connection from creating an
	// unbounded number of channels.
	MaxChannelsPerConnection = 100

	// ClientEventLimit / ClientEventWindow implement a fixed-window rate limit
	// for client-originated events on a single connection.
	ClientEventLimit  = 100
	ClientEventWindow = 10 * time.Second
)

// channelNameRegex permits the characters allowed by the Pusher protocol:
// letters, digits and _ - = @ , . ; (with the private-/presence- prefixes
// expressed through the same set).
var channelNameRegex = regexp.MustCompile(`^[A-Za-z0-9_\-=@,\.;]+$`)

// IsValidChannelName reports whether name is a syntactically valid, bounded
// Pusher channel name. Enforcing this on every subscribe/unsubscribe/event
// path stops clients from filling the hub with arbitrary channel keys.
func IsValidChannelName(name string) bool {
	if len(name) == 0 || len(name) > MaxChannelNameLength {
		return false
	}
	return channelNameRegex.MatchString(name)
}

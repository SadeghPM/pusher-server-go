package core

import (
	"strings"
	"testing"
)

func TestIsValidChannelName(t *testing.T) {
	valid := []string{
		"a",
		"my-channel",
		"private-orders.42",
		"presence-chat.7",
		"news@feed,comments;sports=live",
		strings.Repeat("a", MaxChannelNameLength),
	}
	for _, name := range valid {
		if !IsValidChannelName(name) {
			t.Errorf("Expected channel %q to be valid", name)
		}
	}

	invalid := []string{
		"",
		strings.Repeat("a", MaxChannelNameLength+1),
		"bad name",
		"bad\nname",
		`bad"name`,
		"bad/name",
		"<script>",
		"../etc/passwd",
	}
	for _, name := range invalid {
		if IsValidChannelName(name) {
			t.Errorf("Expected channel %q to be invalid", name)
		}
	}
}

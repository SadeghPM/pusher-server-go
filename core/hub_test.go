package core

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

type recordingDispatcher struct {
	mu     sync.Mutex
	events []WebhookEvent
}

func (d *recordingDispatcher) Dispatch(_ string, events []WebhookEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, events...)
}

func (d *recordingDispatcher) eventNames() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	names := make([]string, len(d.events))
	for i, event := range d.events {
		names[i] = event.Name
	}
	return names
}

func TestAppHubRegistration(t *testing.T) {
	hub := NewAppHub("test-app", nil)
	client := &Client{SocketID: "123.456", Send: make(chan []byte)}

	hub.RegisterClient(client)

	if len(hub.clients) != 1 {
		t.Errorf("Expected 1 client, got %d", len(hub.clients))
	}

	hub.UnregisterClient(client)

	if len(hub.clients) != 0 {
		t.Errorf("Expected 0 clients, got %d", len(hub.clients))
	}
}

func TestAppHubSubscription(t *testing.T) {
	hub := NewAppHub("test-app", nil)
	client := &Client{SocketID: "123.456", Send: make(chan []byte)}
	hub.RegisterClient(client)

	hub.Subscribe(client, "my-channel", nil)

	if len(hub.channels["my-channel"]) != 1 {
		t.Errorf("Expected 1 subscriber in channel, got %d", len(hub.channels["my-channel"]))
	}
}

func TestTrySubscribeEnforcesPresenceCap(t *testing.T) {
	hub := NewAppHub("test-app", nil)
	defer hub.Close()

	const channel = "presence-capped"
	for i := 0; i < MaxPresenceMembersPerChannel; i++ {
		client := &Client{SocketID: fmt.Sprintf("1.%d", i), Send: make(chan []byte, 1)}
		hub.RegisterClient(client)
		if _, accepted := hub.TrySubscribe(client, channel, &ChannelMember{UserID: fmt.Sprintf("user-%d", i)}); !accepted {
			t.Fatalf("Member %d was rejected before reaching the cap", i)
		}
	}

	extra := &Client{SocketID: "1.extra", Send: make(chan []byte, 1)}
	if _, accepted := hub.TrySubscribe(extra, channel, &ChannelMember{UserID: "extra"}); accepted {
		t.Error("Expected a new user to be rejected once the presence cap is reached")
	}

	// An existing user reconnecting from another connection stays allowed.
	returning := &Client{SocketID: "1.returning", Send: make(chan []byte, 1)}
	if _, accepted := hub.TrySubscribe(returning, channel, &ChannelMember{UserID: "user-0"}); !accepted {
		t.Error("Expected an existing presence user to be accepted")
	}
}

func TestPresenceCapCountsDistinctUsers(t *testing.T) {
	hub := NewAppHub("test-app", nil)
	defer hub.Close()

	const channel = "presence-tabs"
	// Many connections for the same user must not consume the member budget.
	for i := 0; i < MaxPresenceMembersPerChannel; i++ {
		client := &Client{SocketID: fmt.Sprintf("2.%d", i), Send: make(chan []byte, 1)}
		if _, accepted := hub.TrySubscribe(client, channel, &ChannelMember{UserID: "same-user"}); !accepted {
			t.Fatalf("Tab %d was rejected even though only one distinct user is present", i)
		}
	}

	// A genuinely new user still fits: the cap is 100 distinct users, not
	// 100 connections.
	newClient := &Client{SocketID: "2.new", Send: make(chan []byte, 1)}
	if _, accepted := hub.TrySubscribe(newClient, channel, &ChannelMember{UserID: "new-user"}); !accepted {
		t.Error("Expected a new user to be accepted when the channel has one distinct user")
	}
}

func TestSubscriptionInfoTracksChannelCount(t *testing.T) {
	hub := NewAppHub("test-app", nil)
	defer hub.Close()

	client := &Client{SocketID: "3.1", Send: make(chan []byte, 1)}
	for _, channel := range []string{"one", "two", "three"} {
		if _, accepted := hub.TrySubscribe(client, channel, nil); !accepted {
			t.Fatalf("Failed to subscribe to %q", channel)
		}
	}

	count, subscribed := hub.SubscriptionInfo(client, "two")
	if count != 3 || !subscribed {
		t.Fatalf("SubscriptionInfo() = (%d, %v), want (3, true)", count, subscribed)
	}

	// Re-subscribing to an existing channel must not inflate the count.
	hub.TrySubscribe(client, "two", nil)
	if count, _ := hub.SubscriptionInfo(client, "two"); count != 3 {
		t.Errorf("Re-subscribe changed the channel count to %d", count)
	}

	hub.Unsubscribe(client, "two")
	count, subscribed = hub.SubscriptionInfo(client, "two")
	if count != 2 || subscribed {
		t.Errorf("After unsubscribe, SubscriptionInfo() = (%d, %v), want (2, false)", count, subscribed)
	}
}

func TestAppHubUnsubscribeNonExistentChannel(t *testing.T) {
	hub := NewAppHub("test-app", nil)
	client := &Client{SocketID: "123.456", Send: make(chan []byte)}
	hub.RegisterClient(client)

	// Attempt to unsubscribe from a channel that doesn't exist
	hub.Unsubscribe(client, "non-existent-channel")

	if len(hub.channels) != 0 {
		t.Errorf("Expected 0 channels, got %d", len(hub.channels))
	}
}

func TestAppHubUnsubscribeBroadcastsMemberRemoved(t *testing.T) {
	hub := NewAppHub("test-app", nil)
	defer hub.Close()

	client1 := &Client{SocketID: "123.1", Send: make(chan []byte, 4)}
	client2 := &Client{SocketID: "123.2", Send: make(chan []byte, 4)}
	hub.RegisterClient(client1)
	hub.RegisterClient(client2)

	const channel = `presence-"chat"`
	hub.Subscribe(client1, channel, &ChannelMember{UserID: `user-"one"`})
	hub.Subscribe(client2, channel, &ChannelMember{UserID: "user-two"})

	hub.Unsubscribe(client1, channel)

	select {
	case payload := <-client2.Send:
		if !json.Valid(payload) {
			t.Fatalf("member_removed payload is invalid JSON: %s", payload)
		}

		var event Event
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatalf("Failed to unmarshal member_removed: %v", err)
		}
		if event.Event != "pusher_internal:member_removed" {
			t.Errorf("Unexpected event: %q", event.Event)
		}
		if event.Channel != channel {
			t.Errorf("Unexpected channel: %q", event.Channel)
		}

		var dataStr string
		if err := json.Unmarshal(event.Data, &dataStr); err != nil {
			t.Fatalf("Expected string-encoded data, got %s: %v", event.Data, err)
		}
		var data map[string]string
		if err := json.Unmarshal([]byte(dataStr), &data); err != nil {
			t.Fatalf("Failed to unmarshal member_removed data: %v", err)
		}
		if data["user_id"] != `user-"one"` {
			t.Errorf("Unexpected user_id: %q", data["user_id"])
		}
	case <-time.After(time.Second):
		t.Fatal("Timed out waiting for member_removed broadcast")
	}
}

func TestAppHubCloseIsIdempotent(t *testing.T) {
	hub := NewAppHub("test-app", nil)

	hub.Close()
	hub.Close()

	// Enqueueing after close must not panic or block.
	hub.enqueue(func() {})

	if len(hub.clients) != 0 {
		t.Errorf("Expected no clients after close, got %d", len(hub.clients))
	}
}

func TestHubCloseFlushesCleanupWebhooksBeforeStoppingQueue(t *testing.T) {
	dispatcher := &recordingDispatcher{}
	hub := NewAppHub("test-app", dispatcher)

	client := &Client{SocketID: "1.1", Send: make(chan []byte, 8)}
	hub.RegisterClient(client)
	if _, accepted := hub.Subscribe(client, "presence-chat", &ChannelMember{UserID: "user-1"}); !accepted {
		t.Fatal("Expected presence subscription to be accepted")
	}

	// Close must wait for the read pump's unregister work even though this
	// test client has no real connection.
	hub.Close()

	got := dispatcher.eventNames()
	want := []string{"channel_occupied", "member_removed", "channel_vacated"}
	if len(got) != len(want) {
		t.Fatalf("Dispatched webhooks = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Dispatched webhooks = %v, want %v", got, want)
		}
	}
}

func TestAsyncBroadcastsPreserveOrder(t *testing.T) {
	hub := NewAppHub("test-app", nil)
	defer hub.Close()

	client := &Client{SocketID: "1.1", Send: make(chan []byte, 4)}
	hub.RegisterClient(client)
	hub.Subscribe(client, "ordered-channel", nil)

	hub.BroadcastToChannelAsync("ordered-channel", []byte("first"), "")
	hub.BroadcastToChannelAsync("ordered-channel", []byte("second"), "")

	for _, want := range []string{"first", "second"} {
		select {
		case msg := <-client.Send:
			if string(msg) != want {
				t.Fatalf("Broadcast order = %q, want %q", msg, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("Timed out waiting for %q", want)
		}
	}
}

func TestSubscribeAckIsQueuedBeforeChannelBroadcasts(t *testing.T) {
	hub := NewAppHub("test-app", nil)
	defer hub.Close()

	client := &Client{SocketID: "1.1", Send: make(chan []byte, 4)}
	hub.RegisterClient(client)

	enqueued := make(chan struct{})
	started := make(chan struct{})
	go func() {
		<-started
		hub.BroadcastToChannelAsync("ordering", []byte("broadcast"), "")
		close(enqueued)
	}()

	_, accepted := hub.TrySubscribeWithAck(client, "ordering", nil, func(map[string]json.RawMessage) {
		close(started)
		<-enqueued
		client.TrySend([]byte("ack"))
	})
	if !accepted {
		t.Fatal("Expected subscription to be accepted")
	}

	for _, want := range []string{"ack", "broadcast"} {
		select {
		case msg := <-client.Send:
			if string(msg) != want {
				t.Fatalf("Message order = %q, want %q", msg, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("Timed out waiting for %q", want)
		}
	}
}

func TestGlobalHubRemoveAppHub(t *testing.T) {
	global := NewGlobalHub(nil, nil)
	hub := global.GetOrCreateAppHub("app1")

	global.RemoveAppHub("app1")
	global.RemoveAppHub("app1") // must be idempotent

	if global.GetAppHub("app1") != nil {
		t.Fatal("Expected app hub to be removed")
	}

	// A new hub must not reuse the closed instance.
	replacement := global.GetOrCreateAppHub("app1")
	if replacement == hub {
		t.Fatal("Expected a fresh app hub after removal")
	}
	replacement.Close()
}

func TestGlobalHub(t *testing.T) {
	global := NewGlobalHub(nil, nil)

	hub1 := global.GetOrCreateAppHub("app1")
	hub2 := global.GetOrCreateAppHub("app2")

	if hub1.AppID() != "app1" || hub2.AppID() != "app2" {
		t.Errorf("GlobalHub created apps with wrong IDs")
	}

	retrievedHub := global.GetAppHub("app1")
	if retrievedHub != hub1 {
		t.Errorf("Expected to retrieve the same hub instance")
	}
}

func TestAppHubGetPresenceMembers(t *testing.T) {
	hub := NewAppHub("test-app", nil)

	// Create some clients
	client1 := &Client{SocketID: "123.456", Send: make(chan []byte)}
	client2 := &Client{SocketID: "123.457", Send: make(chan []byte)}
	client3 := &Client{SocketID: "123.458", Send: make(chan []byte)}

	hub.RegisterClient(client1)
	hub.RegisterClient(client2)
	hub.RegisterClient(client3)

	channelName := "presence-chat"

	// Test on empty channel
	members := hub.GetPresenceMembers(channelName)
	if len(members) != 0 {
		t.Errorf("Expected 0 presence members, got %d", len(members))
	}

	// Add user 1
	member1 := &ChannelMember{
		UserID:   "user1",
		UserInfo: []byte(`{"name":"Alice"}`),
	}
	hub.Subscribe(client1, channelName, member1)

	members = hub.GetPresenceMembers(channelName)
	if len(members) != 1 {
		t.Errorf("Expected 1 presence member, got %d", len(members))
	}
	if string(members["user1"]) != `{"name":"Alice"}` {
		t.Errorf("Expected user1 info to be `{\"name\":\"Alice\"}`, got %s", string(members["user1"]))
	}

	// Add user 2
	member2 := &ChannelMember{
		UserID:   "user2",
		UserInfo: []byte(`{"name":"Bob"}`),
	}
	hub.Subscribe(client2, channelName, member2)

	// Add another connection for user 1
	member1_copy := &ChannelMember{
		UserID:   "user1",
		UserInfo: []byte(`{"name":"Alice"}`), // Same user info
	}
	hub.Subscribe(client3, channelName, member1_copy)

	members = hub.GetPresenceMembers(channelName)
	if len(members) != 2 {
		t.Errorf("Expected 2 unique presence members, got %d", len(members))
	}
	if string(members["user1"]) != `{"name":"Alice"}` {
		t.Errorf("Expected user1 info to be `{\"name\":\"Alice\"}`, got %s", string(members["user1"]))
	}
	if string(members["user2"]) != `{"name":"Bob"}` {
		t.Errorf("Expected user2 info to be `{\"name\":\"Bob\"}`, got %s", string(members["user2"]))
	}
}

func TestAppHubBroadcastToChannelAfterClientClose(t *testing.T) {
	// Regression: UnregisterClient closes the client's Send channel while a
	// concurrent BroadcastToChannel may still be iterating subscribers. A send
	// racing with that close used to panic; TrySend must make it safe.
	hub := NewAppHub("test-app", nil)
	defer hub.Close()

	client := &Client{SocketID: "123.1", Send: make(chan []byte, 1)}
	hub.RegisterClient(client)
	hub.Subscribe(client, "race-channel", nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		payload := []byte(`{"event":"e","data":"{}"}`)
		for i := 0; i < 1000; i++ {
			hub.BroadcastToChannel("race-channel", payload, "")
		}
	}()

	for i := 0; i < 1000; i++ {
		hub.UnregisterClient(client)
		hub.RegisterClient(client)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Timed out: broadcast never completed")
	}
}

func TestAppHubBroadcastToChannel(t *testing.T) {
	hub := NewAppHub("test-app", nil)
	channelName := "test-channel"

	// Create clients with buffered channels so sends don't block
	client1 := &Client{SocketID: "123.1", Send: make(chan []byte, 1)}
	client2 := &Client{SocketID: "123.2", Send: make(chan []byte, 1)}
	client3 := &Client{SocketID: "123.3", Send: make(chan []byte, 1)}

	// Create a client with a full buffer to test non-blocking behavior
	clientFull := &Client{SocketID: "123.full", Send: make(chan []byte, 1)}
	clientFull.Send <- []byte("initial")

	hub.RegisterClient(client1)
	hub.RegisterClient(client2)
	hub.RegisterClient(client3)
	hub.RegisterClient(clientFull)

	hub.Subscribe(client1, channelName, nil)
	hub.Subscribe(client2, channelName, nil)
	hub.Subscribe(client3, channelName, nil)
	hub.Subscribe(clientFull, channelName, nil)

	payload := []byte(`{"event":"my_event","data":"hello"}`)

	// Broadcast to the channel, excluding client2
	hub.BroadcastToChannel(channelName, payload, client2.SocketID)

	// Check client1 (should receive)
	select {
	case msg := <-client1.Send:
		if string(msg) != string(payload) {
			t.Errorf("Client 1 expected %s, got %s", string(payload), string(msg))
		}
	default:
		t.Errorf("Client 1 did not receive message")
	}

	// Check client2 (should be excluded)
	select {
	case <-client2.Send:
		t.Errorf("Client 2 received message but should have been excluded")
	default:
		// Expected
	}

	// Check client3 (should receive)
	select {
	case msg := <-client3.Send:
		if string(msg) != string(payload) {
			t.Errorf("Client 3 expected %s, got %s", string(payload), string(msg))
		}
	default:
		t.Errorf("Client 3 did not receive message")
	}

	// Check clientFull (should not receive, but should not block broadcast)
	select {
	case msg := <-clientFull.Send:
		if string(msg) != "initial" {
			t.Errorf("Client full expected initial message, got %s", string(msg))
		}
	default:
		t.Errorf("Client full channel should have had initial message")
	}
}

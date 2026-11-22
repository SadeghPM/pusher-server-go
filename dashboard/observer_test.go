package dashboard

import "testing"

func TestObserverIsolatesAppStreams(t *testing.T) {
	observer := NewObserver()

	watched := observer.Subscribe("app-a")
	defer observer.Unsubscribe("app-a", watched)
	noisy := observer.Subscribe("app-b")
	defer observer.Unsubscribe("app-b", noisy)

	// Overflow app-b's buffer; it must not consume app-a's capacity.
	for i := 0; i < 150; i++ {
		observer.Notify(DebugEvent{AppID: "app-b", Type: "noise"})
	}
	observer.Notify(DebugEvent{AppID: "app-a", Type: "important"})

	select {
	case event := <-watched:
		if event.AppID != "app-a" || event.Type != "important" {
			t.Fatalf("Watched app received %+v", event)
		}
	default:
		t.Fatal("Watched app did not receive its event")
	}

	if got := len(noisy); got != 100 {
		t.Errorf("Noisy app buffer length = %d, want 100", got)
	}
}

func TestObserverUnsubscribeOnlyClosesMatchingStream(t *testing.T) {
	observer := NewObserver()

	a := observer.Subscribe("app-a")
	b := observer.Subscribe("app-a")

	observer.Unsubscribe("app-a", a)

	if _, ok := <-a; ok {
		t.Fatal("Expected the unsubscribed channel to be closed")
	}
	observer.Notify(DebugEvent{AppID: "app-a", Type: "still-open"})
	select {
	case event := <-b:
		if event.Type != "still-open" {
			t.Fatalf("Unexpected event: %+v", event)
		}
	default:
		t.Fatal("Remaining subscriber stopped receiving events")
	}
	observer.Unsubscribe("app-a", b)
}

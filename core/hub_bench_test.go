package core

import (
	"fmt"
	"testing"
)

func BenchmarkUnregisterClient(b *testing.B) {
	for _, numChannels := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("Channels-%d", numChannels), func(b *testing.B) {
			hub := NewAppHub("bench-app")

			// Create channels with some other clients
			for j := 0; j < numChannels; j++ {
				channelName := fmt.Sprintf("channel-%d", j)
				dummyClient := &Client{SocketID: fmt.Sprintf("dummy-%d", j), Send: make(chan []byte, 1)}
				hub.Subscribe(dummyClient, channelName, nil)
			}

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				b.StopTimer()
				// Create our target client subscribed to just 1 channel
				targetClient := &Client{SocketID: "target", Send: make(chan []byte, 1)}
				hub.RegisterClient(targetClient)
				hub.Subscribe(targetClient, "channel-0", nil)

				b.StartTimer()
				hub.UnregisterClient(targetClient)
			}
		})
	}
}

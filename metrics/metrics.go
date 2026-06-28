package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	ActiveConnections = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pusher_active_connections",
			Help: "Current number of active WebSocket connections per app",
		},
		[]string{"app_id"},
	)

	MessagesPublishedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pusher_messages_published_total",
			Help: "Total number of messages published per app",
		},
		[]string{"app_id"},
	)

	WebsocketErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pusher_websocket_errors_total",
			Help: "Total number of WebSocket errors per app",
		},
		[]string{"app_id", "type"},
	)

	RestAPIEventsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pusher_rest_api_events_total",
			Help: "Total number of events published via the REST API per app",
		},
		[]string{"app_id"},
	)

	ChannelsActive = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pusher_channels_active",
			Help: "Current number of active channels per app",
		},
		[]string{"app_id"},
	)
)

package handler

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	AlertsConsumedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "alerts_consumed_total",
			Help: "Total number of alerts consumed from RabbitMQ",
		},
		[]string{"status"},
	)

	AlertsBroadcastTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "alerts_broadcast_total",
			Help: "Total number of alerts broadcasted via WebSocket",
		},
		[]string{"recipients"},
	)

	WebSocketConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "websocket_connections",
			Help: "Current number of WebSocket connections",
		},
		[]string{"role"},
	)

	RabbitMQConsumeDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "rabbitmq_consume_duration_seconds",
			Help:    "Duration of RabbitMQ message consumption",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		},
		[]string{"status"},
	)
)

// RegisterAlertConsumerMetrics registers all alert-consumer metrics
func RegisterAlertConsumerMetrics() {
	prometheus.MustRegister(AlertsConsumedTotal)
	prometheus.MustRegister(AlertsBroadcastTotal)
	prometheus.MustRegister(WebSocketConnections)
	prometheus.MustRegister(RabbitMQConsumeDuration)
}


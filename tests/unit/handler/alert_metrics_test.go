package handler_test

import (
	"testing"

	"github.com/IANDYI/alert-consumer/internal/adapters/handler"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func TestRegisterAlertConsumerMetrics(t *testing.T) {
	// Create a custom registry for testing
	registry := prometheus.NewRegistry()

	// Register metrics to the custom registry
	registry.MustRegister(handler.AlertsConsumedTotal)
	registry.MustRegister(handler.AlertsBroadcastTotal)
	registry.MustRegister(handler.WebSocketConnections)
	registry.MustRegister(handler.RabbitMQConsumeDuration)

	// Gather metrics from the registry
	metrics, err := registry.Gather()
	assert.NoError(t, err)

	foundAlertsConsumed := false
	foundAlertsBroadcast := false
	foundWebSocketConnections := false
	foundRabbitMQConsumeDuration := false

	for _, metric := range metrics {
		switch metric.GetName() {
		case "alerts_consumed_total":
			foundAlertsConsumed = true
		case "alerts_broadcast_total":
			foundAlertsBroadcast = true
		case "websocket_connections":
			foundWebSocketConnections = true
		case "rabbitmq_consume_duration_seconds":
			foundRabbitMQConsumeDuration = true
		}
	}

	assert.True(t, foundAlertsConsumed, "AlertsConsumedTotal metric should be registered")
	assert.True(t, foundAlertsBroadcast, "AlertsBroadcastTotal metric should be registered")
	assert.True(t, foundWebSocketConnections, "WebSocketConnections metric should be registered")
	assert.True(t, foundRabbitMQConsumeDuration, "RabbitMQConsumeDuration metric should be registered")
}

func TestMetrics_AlertsConsumedTotal(t *testing.T) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(handler.AlertsConsumedTotal)

	handler.AlertsConsumedTotal.WithLabelValues("success").Inc()
	handler.AlertsConsumedTotal.WithLabelValues("failed").Inc()
	handler.AlertsConsumedTotal.WithLabelValues("success").Inc()

	// Verify metric can be incremented without error
	assert.NotNil(t, handler.AlertsConsumedTotal)
}

func TestMetrics_AlertsBroadcastTotal(t *testing.T) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(handler.AlertsBroadcastTotal)

	handler.AlertsBroadcastTotal.WithLabelValues("connected").Inc()
	handler.AlertsBroadcastTotal.WithLabelValues("none").Inc()

	assert.NotNil(t, handler.AlertsBroadcastTotal)
}

func TestMetrics_WebSocketConnections(t *testing.T) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(handler.WebSocketConnections)

	handler.WebSocketConnections.WithLabelValues("admin").Set(5)
	handler.WebSocketConnections.WithLabelValues("user").Set(10)

	assert.NotNil(t, handler.WebSocketConnections)
}

func TestMetrics_RabbitMQConsumeDuration(t *testing.T) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(handler.RabbitMQConsumeDuration)

	handler.RabbitMQConsumeDuration.WithLabelValues("success").Observe(0.1)
	handler.RabbitMQConsumeDuration.WithLabelValues("failed").Observe(0.2)

	assert.NotNil(t, handler.RabbitMQConsumeDuration)
}

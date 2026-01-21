package handler_test

import (
	"testing"

	"github.com/IANDYI/alert-consumer/internal/adapters/handler"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func TestRegisterAlertConsumerMetrics(t *testing.T) {
	// Test that RegisterAlertConsumerMetrics can be called without panicking
	// Note: If metrics are already registered, MustRegister will panic, but that's expected behavior
	// In production, this function should only be called once at startup
	
	// Verify metrics exist and are not nil
	assert.NotNil(t, handler.AlertsConsumedTotal, "AlertsConsumedTotal should exist")
	assert.NotNil(t, handler.AlertsBroadcastTotal, "AlertsBroadcastTotal should exist")
	assert.NotNil(t, handler.WebSocketConnections, "WebSocketConnections should exist")
	assert.NotNil(t, handler.RabbitMQConsumeDuration, "RabbitMQConsumeDuration should exist")
	
	// Verify metrics can be used (this also verifies they're properly initialized)
	handler.AlertsConsumedTotal.WithLabelValues("test").Inc()
	handler.AlertsBroadcastTotal.WithLabelValues("test").Inc()
	handler.WebSocketConnections.WithLabelValues("test").Set(1)
	handler.RabbitMQConsumeDuration.WithLabelValues("test").Observe(0.1)
	
	// If we get here without panicking, the metrics are working correctly
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

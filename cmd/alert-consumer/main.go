package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/IANDYI/alert-consumer/internal/adapters/handler"
	"github.com/IANDYI/alert-consumer/internal/adapters/middleware"
	"github.com/IANDYI/alert-consumer/internal/adapters/websocket"
	"github.com/IANDYI/alert-consumer/internal/config"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rabbitmq/amqp091-go"
)

type AlertEvent struct {
	BabyID       string                 `json:"baby_id"`
	Measurement  map[string]interface{} `json:"measurement"`
	Timestamp    time.Time              `json:"timestamp"`
	AlertType    string                 `json:"alert_type"`
	SafetyStatus string                 `json:"safety_status"`
	Severity     string                 `json:"severity,omitempty"`
}

type AlertConsumer struct {
	rabbitMQURL string
	queueName   string
	conn        *amqp091.Connection
	channel     *amqp091.Channel
	hub         *websocket.Hub
	alerts      []AlertEvent
	alertCount  int
	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	httpServer  *http.Server
	authMW      *middleware.AuthMiddleware
}

func main() {
	cfg := config.LoadAlertConsumerConfig()

	log.Printf("Starting alert consumer - RabbitMQ: %s, Queue: %s", cfg.RabbitMQURL, cfg.QueueName)

	hub := websocket.NewHub()
	go hub.Run()

	authMiddleware := middleware.NewAuthMiddleware(cfg.JWTPublicKey)

	ctx, cancel := context.WithCancel(context.Background())
	consumer := &AlertConsumer{
		rabbitMQURL: cfg.RabbitMQURL,
		queueName:   cfg.QueueName,
		hub:         hub,
		alerts:      make([]AlertEvent, 0),
		ctx:         ctx,
		cancel:      cancel,
		authMW:      authMiddleware,
	}

	handler.RegisterAlertConsumerMetrics()

	// Start HTTP server in goroutine
	go consumer.startWebSocketServer(cfg)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go consumer.startWithReconnection()

	// Give server time to start
	time.Sleep(500 * time.Millisecond)
	log.Println("Alert consumer is starting...")

	<-sigChan
	log.Printf("\nShutting down alert consumer...")

	// Stop consuming new messages
	cancel()

	// Shutdown auth middleware janitor
	if consumer.authMW != nil {
		consumer.authMW.Stop()
		log.Println("Auth middleware janitor stopped")
	}

	// Shutdown HTTP server gracefully
	if consumer.httpServer != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()

		log.Println("Shutting down HTTP server...")
		if err := consumer.httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP server forced to shutdown: %v", err)
		} else {
			log.Println("HTTP server shutdown complete")
		}
	}

	// Close RabbitMQ connections
	consumer.close()

	consumer.mu.Lock()
	alertCount := consumer.alertCount
	alerts := make([]AlertEvent, len(consumer.alerts))
	copy(alerts, consumer.alerts)
	consumer.mu.Unlock()

	if err := writeAlertsToFile(alerts); err != nil {
		log.Printf("Failed to write final alerts to file: %v", err)
	} else {
		log.Printf("Final alerts written to file successfully")
	}

	log.Printf("Total alerts received: %d", alertCount)
	log.Println("Alert consumer shutdown complete")
}

func (c *AlertConsumer) startWebSocketServer(cfg *config.AlertConsumerConfig) {
	mux := http.NewServeMux()

	websocketHandler := handler.NewWebSocketHandler(c.hub, c.authMW, cfg.AllowedOrigins)
	mux.HandleFunc("GET /ws", websocketHandler.HandleWebSocket)

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
			log.Printf("Error encoding health response: %v", err)
		}
	})

	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]string{"status": "alive"}); err != nil {
			log.Printf("Error encoding liveness response: %v", err)
		}
	})

	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]string{"status": "ready"}); err != nil {
			log.Printf("Error encoding readiness response: %v", err)
		}
	})

	mux.HandleFunc("GET /metrics", promhttp.Handler().ServeHTTP)

	c.httpServer = &http.Server{
		Addr:         ":" + cfg.WebSocketPort,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("WebSocket server listening on :%s", cfg.WebSocketPort)
	if err := c.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("WebSocket server failed: %v", err)
	}
}

// startWithReconnection handles connection and automatic reconnection
func (c *AlertConsumer) startWithReconnection() {
	retryDelay := 5 * time.Second

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		if err := c.connect(); err != nil {
			log.Printf("Failed to connect to RabbitMQ: %v, retrying in %v...", err, retryDelay)
			time.Sleep(retryDelay)
			continue
		}

		if err := c.consume(); err != nil {
			log.Printf("Consumer error: %v, reconnecting...", err)
			c.close()
			time.Sleep(retryDelay)
			continue
		}

		log.Println("Connection lost, attempting to reconnect...")
		c.close()
		time.Sleep(retryDelay)
	}
}

func (c *AlertConsumer) connect() error {
	var err error
	maxRetries := 3
	retryDelay := 2 * time.Second

	for i := 0; i < maxRetries; i++ {
		c.conn, err = amqp091.Dial(c.rabbitMQURL)
		if err == nil {
			break
		}
		log.Printf("Failed to connect to RabbitMQ (attempt %d/%d): %v", i+1, maxRetries, err)
		if i < maxRetries-1 {
			time.Sleep(retryDelay)
		}
	}

	if err != nil {
		return fmt.Errorf("failed to connect after %d attempts: %w", maxRetries, err)
	}

	c.channel, err = c.conn.Channel()
	if err != nil {
		c.conn.Close()
		return fmt.Errorf("failed to open channel: %w", err)
	}

	_, err = c.channel.QueueDeclare(
		c.queueName, // name
		true,        // durable
		false,       // delete when unused
		false,       // exclusive
		false,       // no-wait
		nil,         // arguments
	)
	if err != nil {
		c.channel.Close()
		c.conn.Close()
		return fmt.Errorf("failed to declare queue: %w", err)
	}

	err = c.channel.Qos(
		1,     // prefetch count
		0,     // prefetch size
		false, // global
	)
	if err != nil {
		c.channel.Close()
		c.conn.Close()
		return fmt.Errorf("failed to set QoS: %w", err)
	}

	log.Println("Connected to RabbitMQ successfully")
	return nil
}

func (c *AlertConsumer) consume() error {
	msgs, err := c.channel.Consume(
		c.queueName, // queue
		"",          // consumer tag
		false,       // auto-ack (manual acknowledgment)
		false,       // exclusive
		false,       // no-local
		false,       // no-wait
		nil,         // args
	)
	if err != nil {
		return fmt.Errorf("failed to register consumer: %w", err)
	}

	log.Printf("Alert consumer started. Waiting for alerts...")

	for {
		select {
		case <-c.ctx.Done():
			log.Println("Consumer context cancelled, stopping message processing")
			return nil
		case msg, ok := <-msgs:
			if !ok {
				return fmt.Errorf("message channel closed")
			}

			if err := c.processMessage(msg); err != nil {
				log.Printf("Error processing message: %v", err)
				if err := msg.Nack(false, true); err != nil {
					log.Printf("Error nacking message: %v", err)
				}
				handler.AlertsConsumedTotal.WithLabelValues("failed").Inc()
				continue
			}

			if err := msg.Ack(false); err != nil {
				log.Printf("Failed to acknowledge message: %v", err)
			} else {
				handler.AlertsConsumedTotal.WithLabelValues("success").Inc()
			}
		}
	}
}

func (c *AlertConsumer) processMessage(msg amqp091.Delivery) error {
	startTime := time.Now()

	var alert AlertEvent
	if err := json.Unmarshal(msg.Body, &alert); err != nil {
		log.Printf("Error unmarshaling alert: %v", err)
		handler.RabbitMQConsumeDuration.WithLabelValues("failed").Observe(time.Since(startTime).Seconds())
		return fmt.Errorf("failed to unmarshal alert: %w", err)
	}

	if alert.BabyID == "" {
		log.Printf("Invalid alert: missing baby_id")
		handler.RabbitMQConsumeDuration.WithLabelValues("failed").Observe(time.Since(startTime).Seconds())
		return fmt.Errorf("invalid alert: missing baby_id")
	}

	c.mu.Lock()
	c.alertCount++
	c.alerts = append(c.alerts, alert)
	alertsCopy := make([]AlertEvent, len(c.alerts))
	copy(alertsCopy, c.alerts)
	c.mu.Unlock()

	log.Printf("=== ALERT RECEIVED #%d ===", c.alertCount)
	log.Printf("Baby ID: %s", alert.BabyID)
	log.Printf("Alert Type: %s", alert.AlertType)
	log.Printf("Safety Status: %s", alert.SafetyStatus)
	log.Printf("Severity: %s", alert.Severity)
	log.Printf("Timestamp: %s", alert.Timestamp.Format(time.RFC3339))

	if measurement, ok := alert.Measurement["type"].(string); ok {
		log.Printf("Measurement Type: %s", measurement)
	}
	if value, ok := alert.Measurement["value"].(float64); ok {
		log.Printf("Measurement Value: %.2f", value)
	}
	log.Printf("========================\n")

	alertJSON, err := json.Marshal(alert)
	if err != nil {
		log.Printf("Failed to marshal alert for broadcast: %v", err)
	} else {
		connectedCount := c.hub.GetConnectedAdminCount()
		if connectedCount > 0 {
			c.hub.BroadcastToAdmins(alertJSON)
			handler.AlertsBroadcastTotal.WithLabelValues("connected").Inc()
		} else {
			log.Printf("No connected admin/nurses - alert consumed but not broadcasted")
			handler.AlertsBroadcastTotal.WithLabelValues("none").Inc()
		}
	}

	if err := writeAlertsToFile(alertsCopy); err != nil {
		log.Printf("Failed to write alerts to file: %v", err)
	}

	handler.RabbitMQConsumeDuration.WithLabelValues("success").Observe(time.Since(startTime).Seconds())
	return nil
}

func (c *AlertConsumer) close() {
	if c.channel != nil && !c.channel.IsClosed() {
		if err := c.channel.Close(); err != nil {
			log.Printf("Error closing RabbitMQ channel: %v", err)
		}
	}

	if c.conn != nil && !c.conn.IsClosed() {
		if err := c.conn.Close(); err != nil {
			log.Printf("Error closing RabbitMQ connection: %v", err)
		}
	}
}

func writeAlertsToFile(alerts []AlertEvent) error {
	file, err := os.Create("/tmp/alerts_received.json")
	if err != nil {
		return fmt.Errorf("failed to create alerts file: %w", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(alerts); err != nil {
		return fmt.Errorf("failed to write alerts: %w", err)
	}

	return nil
}


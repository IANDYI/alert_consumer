package config

import (
	"crypto/rsa"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type AlertConsumerConfig struct {
	JWTPublicKey   *rsa.PublicKey
	RabbitMQURL    string
	QueueName      string
	WebSocketPort  string
	PublicKeyPath  string
	AllowedOrigins []string
}

// LoadAlertConsumerConfig loads configuration for the alert consumer.
// It is intentionally self-contained so that changing care-service config
// does not impact the alert-consumer.
func LoadAlertConsumerConfig() *AlertConsumerConfig {
	publicKeyPath := os.Getenv("PUBLIC_KEY_PATH")
	if publicKeyPath == "" {
		publicKeyPath = "/etc/certs/public.pem"
	}

	publicKey, err := loadPublicKey(publicKeyPath)
	if err != nil {
		publicKey = nil
	}

	rabbitMQURL := os.Getenv("RABBITMQ_URL")
	if rabbitMQURL == "" {
		rabbitMQURL = "amqp://guest:guest@localhost:5672/"
	}

	queueName := os.Getenv("ALERTS_QUEUE_NAME")
	if queueName == "" {
		queueName = "baby_alerts"
	}

	wsPort := os.Getenv("WEBSOCKET_PORT")
	if wsPort == "" {
		wsPort = "8081"
	}

	// Parse allowed origins from environment variable (comma-separated)
	var allowedOrigins []string
	originsEnv := os.Getenv("WEBSOCKET_ALLOWED_ORIGINS")
	if originsEnv != "" {
		allowedOrigins = strings.Split(originsEnv, ",")
		// Trim whitespace from each origin
		for i, origin := range allowedOrigins {
			allowedOrigins[i] = strings.TrimSpace(origin)
		}
	}

	return &AlertConsumerConfig{
		JWTPublicKey:   publicKey,
		RabbitMQURL:    rabbitMQURL,
		QueueName:      queueName,
		WebSocketPort:  wsPort,
		PublicKeyPath:  publicKeyPath,
		AllowedOrigins: allowedOrigins,
	}
}

// loadPublicKey loads an RSA public key from a PEM file.
// This is copied here so the alert-consumer is independent from care-service.
func loadPublicKey(path string) (*rsa.PublicKey, error) {
	keyData, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	publicKey, err := jwt.ParseRSAPublicKeyFromPEM(keyData)
	if err != nil {
		return nil, err
	}
	return publicKey, nil
}


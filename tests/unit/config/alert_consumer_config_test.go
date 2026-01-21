package config_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"

	"github.com/IANDYI/alert-consumer/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTempPublicKeyFile(t *testing.T) string {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	publicKey := &privateKey.PublicKey
	publicKeyDER, err := x509.MarshalPKIXPublicKey(publicKey)
	require.NoError(t, err)

	publicKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyDER,
	})

	tmpFile, err := os.CreateTemp("", "test_public_key_*.pem")
	require.NoError(t, err)
	
	_, err = tmpFile.Write(publicKeyPEM)
	require.NoError(t, err)
	tmpFile.Close()

	return tmpFile.Name()
}

func TestLoadAlertConsumerConfig_Defaults(t *testing.T) {
	// Clear environment variables
	os.Clearenv()

	cfg := config.LoadAlertConsumerConfig()

	assert.NotNil(t, cfg)
	assert.Equal(t, "amqp://guest:guest@localhost:5672/", cfg.RabbitMQURL)
	assert.Equal(t, "baby_alerts", cfg.QueueName)
	assert.Equal(t, "8081", cfg.WebSocketPort)
	assert.Equal(t, "/etc/certs/public.pem", cfg.PublicKeyPath)
	assert.Nil(t, cfg.JWTPublicKey) // Should be nil if file doesn't exist
	assert.Empty(t, cfg.AllowedOrigins)
}

func TestLoadAlertConsumerConfig_FromEnv(t *testing.T) {
	// Set environment variables
	os.Setenv("RABBITMQ_URL", "amqp://test:test@rabbitmq:5672/")
	os.Setenv("ALERTS_QUEUE_NAME", "test_queue")
	os.Setenv("WEBSOCKET_PORT", "9090")
	os.Setenv("PUBLIC_KEY_PATH", "/tmp/test_key.pem")
	os.Setenv("WEBSOCKET_ALLOWED_ORIGINS", "http://localhost:3000,https://example.com")

	defer func() {
		os.Unsetenv("RABBITMQ_URL")
		os.Unsetenv("ALERTS_QUEUE_NAME")
		os.Unsetenv("WEBSOCKET_PORT")
		os.Unsetenv("PUBLIC_KEY_PATH")
		os.Unsetenv("WEBSOCKET_ALLOWED_ORIGINS")
	}()

	cfg := config.LoadAlertConsumerConfig()

	assert.NotNil(t, cfg)
	assert.Equal(t, "amqp://test:test@rabbitmq:5672/", cfg.RabbitMQURL)
	assert.Equal(t, "test_queue", cfg.QueueName)
	assert.Equal(t, "9090", cfg.WebSocketPort)
	assert.Equal(t, "/tmp/test_key.pem", cfg.PublicKeyPath)
	assert.Equal(t, []string{"http://localhost:3000", "https://example.com"}, cfg.AllowedOrigins)
}

func TestLoadAlertConsumerConfig_WithPublicKey(t *testing.T) {
	publicKeyPath := createTempPublicKeyFile(t)
	defer os.Remove(publicKeyPath)

	os.Setenv("PUBLIC_KEY_PATH", publicKeyPath)
	defer os.Unsetenv("PUBLIC_KEY_PATH")

	cfg := config.LoadAlertConsumerConfig()

	assert.NotNil(t, cfg)
	assert.NotNil(t, cfg.JWTPublicKey)
	assert.Equal(t, publicKeyPath, cfg.PublicKeyPath)
}

func TestLoadAlertConsumerConfig_AllowedOrigins_WithSpaces(t *testing.T) {
	os.Setenv("WEBSOCKET_ALLOWED_ORIGINS", "http://localhost:3000 , https://example.com , https://test.com")
	defer os.Unsetenv("WEBSOCKET_ALLOWED_ORIGINS")

	cfg := config.LoadAlertConsumerConfig()

	assert.NotNil(t, cfg)
	assert.Equal(t, []string{"http://localhost:3000", "https://example.com", "https://test.com"}, cfg.AllowedOrigins)
}

func TestLoadAlertConsumerConfig_AllowedOrigins_SingleOrigin(t *testing.T) {
	os.Setenv("WEBSOCKET_ALLOWED_ORIGINS", "http://localhost:3000")
	defer os.Unsetenv("WEBSOCKET_ALLOWED_ORIGINS")

	cfg := config.LoadAlertConsumerConfig()

	assert.NotNil(t, cfg)
	assert.Equal(t, []string{"http://localhost:3000"}, cfg.AllowedOrigins)
}

func TestLoadAlertConsumerConfig_AllowedOrigins_Empty(t *testing.T) {
	os.Setenv("WEBSOCKET_ALLOWED_ORIGINS", "")
	defer os.Unsetenv("WEBSOCKET_ALLOWED_ORIGINS")

	cfg := config.LoadAlertConsumerConfig()

	assert.NotNil(t, cfg)
	assert.Empty(t, cfg.AllowedOrigins)
}

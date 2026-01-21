package websocket_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	gorillaWS "github.com/gorilla/websocket"
	"github.com/IANDYI/alert-consumer/internal/adapters/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewHub(t *testing.T) {
	hub := websocket.NewHub()
	assert.NotNil(t, hub)
	// Note: We can't access unexported fields directly, so we test through exported methods
	assert.Equal(t, 0, hub.GetConnectedAdminCount())
}

func TestNewClient(t *testing.T) {
	hub := websocket.NewHub()
	upgrader := gorillaWS.Upgrader{}
	
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		
		client := websocket.NewClient(hub, conn, "user1", "ADMIN", "admin@test.com", "Admin User")
		assert.NotNil(t, client)
	}))
	defer server.Close()

	url := "ws" + server.URL[4:]
	conn, _, err := gorillaWS.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	defer conn.Close()
}

func TestHub_RegisterClient(t *testing.T) {
	hub := websocket.NewHub()
	go hub.Run()
	defer func() {
		// Give time for goroutines to finish
		time.Sleep(100 * time.Millisecond)
	}()

	upgrader := gorillaWS.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		
		client := websocket.NewClient(hub, conn, "user1", "ADMIN", "admin@test.com", "Admin User")
		hub.RegisterClient(client)
	}))
	defer server.Close()

	url := "ws" + server.URL[4:]
	conn, _, err := gorillaWS.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	defer conn.Close()

	// Give time for registration
	time.Sleep(100 * time.Millisecond)

	// Verify admin count increased
	assert.Equal(t, 1, hub.GetConnectedAdminCount())
}

func TestHub_BroadcastToAdmins(t *testing.T) {
	hub := websocket.NewHub()
	go hub.Run()

	upgrader := gorillaWS.Upgrader{}
	var adminClients []*websocket.Client
	var userClients []*websocket.Client
	var wg sync.WaitGroup

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		
		role := r.URL.Query().Get("role")
		if role == "admin" {
			client := websocket.NewClient(hub, conn, "admin1", "ADMIN", "admin@test.com", "Admin User")
			adminClients = append(adminClients, client)
			hub.RegisterClient(client)
		} else {
			client := websocket.NewClient(hub, conn, "user1", "USER", "user@test.com", "Test User")
			userClients = append(userClients, client)
			hub.RegisterClient(client)
		}
		wg.Done()
	}))
	defer server.Close()

	// Register 1 admin and 1 user
	wg.Add(2)
	url := "ws" + server.URL[4:]
	adminConn, _, err := gorillaWS.DefaultDialer.Dial(url+"?role=admin", nil)
	require.NoError(t, err)
	defer adminConn.Close()

	userConn, _, err := gorillaWS.DefaultDialer.Dial(url+"?role=user", nil)
	require.NoError(t, err)
	defer userConn.Close()

	wg.Wait()
	time.Sleep(100 * time.Millisecond)

	// Verify admin count
	assert.Equal(t, 1, hub.GetConnectedAdminCount())

	// Broadcast to admins only
	message := []byte("admin only message")
	hub.BroadcastToAdmins(message)

	time.Sleep(100 * time.Millisecond)
}

func TestHub_GetConnectedAdminCount(t *testing.T) {
	hub := websocket.NewHub()
	go hub.Run()

	assert.Equal(t, 0, hub.GetConnectedAdminCount())

	upgrader := gorillaWS.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		
		client := websocket.NewClient(hub, conn, "admin1", "ADMIN", "admin@test.com", "Admin User")
		hub.RegisterClient(client)
	}))
	defer server.Close()

	url := "ws" + server.URL[4:]
	conn, _, err := gorillaWS.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	defer conn.Close()

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 1, hub.GetConnectedAdminCount())
}

func TestHub_BroadcastToAdmins_NoAdmins(t *testing.T) {
	hub := websocket.NewHub()
	go hub.Run()

	message := []byte("test message")
	hub.BroadcastToAdmins(message)

	// Should not panic and should log that no admins are connected
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, hub.GetConnectedAdminCount())
}

func TestNewUpgrader(t *testing.T) {
	allowedOrigins := []string{"http://localhost:3000", "https://example.com"}
	upgrader := websocket.NewUpgrader(allowedOrigins)
	assert.NotNil(t, upgrader)
	assert.Equal(t, 1024, upgrader.ReadBufferSize)
	assert.Equal(t, 1024, upgrader.WriteBufferSize)
	assert.NotNil(t, upgrader.CheckOrigin)
}

func TestNewUpgrader_CheckOrigin(t *testing.T) {
	allowedOrigins := []string{"http://localhost:3000", "https://example.com"}
	upgrader := websocket.NewUpgrader(allowedOrigins)

	tests := []struct {
		name   string
		origin string
		want   bool
	}{
		{
			name:   "allowed origin",
			origin: "http://localhost:3000",
			want:   true,
		},
		{
			name:   "another allowed origin",
			origin: "https://example.com",
			want:   true,
		},
		{
			name:   "disallowed origin",
			origin: "https://evil.com",
			want:   false,
		},
		{
			name:   "no origin header",
			origin: "",
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			result := upgrader.CheckOrigin(req)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestNewUpgrader_EmptyAllowedOrigins(t *testing.T) {
	upgrader := websocket.NewUpgrader([]string{})
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://any-origin.com")
	
	// With empty allowed origins, should allow all (with warning in production)
	result := upgrader.CheckOrigin(req)
	assert.True(t, result)
}

func TestUpgrade(t *testing.T) {
	upgrader := gorillaWS.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Upgrade(upgrader, w, r, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer conn.Close()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	url := "ws" + server.URL[4:]
	conn, _, err := gorillaWS.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	defer conn.Close()
}

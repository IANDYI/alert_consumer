package websocket

import (
	"log"
	"net/http"
	"sync"
	"time"

	gorillaWS "github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 512
)

// NewUpgrader creates a WebSocket upgrader with configurable origin checking
func NewUpgrader(allowedOrigins []string) gorillaWS.Upgrader {
	return gorillaWS.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				// If no origin header, allow (may be from same origin or tool)
				return true
			}

			// If no allowed origins configured, allow all (with warning in production)
			if len(allowedOrigins) == 0 {
				return true
			}

			// Check if origin is in allowed list
			for _, allowed := range allowedOrigins {
				if origin == allowed {
					return true
				}
			}

			log.Printf("WebSocket connection rejected: origin %s not in allowed list", origin)
			return false
		},
	}
}

// Client represents a websocket connection
type Client struct {
	hub       *Hub
	conn      *gorillaWS.Conn
	send      chan []byte
	userID    string
	userRole  string
	userEmail string
	userName  string
}

// NewClient creates a new WebSocket client
func NewClient(hub *Hub, conn *gorillaWS.Conn, userID, userRole, userEmail, userName string) *Client {
	return &Client{
		hub:       hub,
		conn:      conn,
		send:      make(chan []byte, 256),
		userID:    userID,
		userRole:  userRole,
		userEmail: userEmail,
		userName:  userName,
	}
}

// Hub maintains the set of active clients and broadcasts messages
type Hub struct {
	clients      map[*Client]bool
	broadcast    chan []byte
	register     chan *Client
	unregister   chan *Client
	mu           sync.RWMutex
	adminClients map[string]*Client
}

// RegisterClient registers a new client with the hub and starts the client pumps
func (h *Hub) RegisterClient(client *Client) {
	h.register <- client
	go client.ReadPump()
	go client.WritePump()
}

// NewHub creates a new WebSocket hub
func NewHub() *Hub {
	return &Hub{
		clients:      make(map[*Client]bool),
		broadcast:    make(chan []byte, 256),
		register:     make(chan *Client),
		unregister:   make(chan *Client),
		adminClients: make(map[string]*Client),
	}
}

// Run starts the hub's main loop
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			if client.userRole == "ADMIN" {
				h.adminClients[client.userID] = client
				log.Printf("Admin/Nurse connected: %s (%s) - UserID: %s (Total: %d)",
					client.userName, client.userEmail, client.userID, len(h.adminClients))
			}
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				if client.userRole == "ADMIN" {
					delete(h.adminClients, client.userID)
					log.Printf("Admin/Nurse disconnected: %s (%s) - UserID: %s (Total: %d)",
						client.userName, client.userEmail, client.userID, len(h.adminClients))
				}
				close(client.send)
			}
			h.mu.Unlock()

		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
					if client.userRole == "ADMIN" {
						delete(h.adminClients, client.userID)
					}
				}
			}
			h.mu.RUnlock()
		}
	}
}

// BroadcastToAdmins sends message only to connected ADMIN users (nurses)
func (h *Hub) BroadcastToAdmins(message []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	sent := 0
	recipients := []string{}

	for userID, client := range h.adminClients {
		select {
		case client.send <- message:
			sent++
			recipients = append(recipients, client.userName+" ("+client.userEmail+")")
		default:
			log.Printf("Failed to send to admin/nurse %s, removing", client.userName)
			close(client.send)
			delete(h.clients, client)
			delete(h.adminClients, userID)
		}
	}

	if sent > 0 {
		log.Printf("Broadcasted alert to %d admin/nurses: %v", sent, recipients)
	} else {
		log.Printf("No connected admin/nurses to receive alert")
	}
}

// GetConnectedAdminCount returns number of connected ADMIN users
func (h *Hub) GetConnectedAdminCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.adminClients)
}

// ReadPump pumps messages from the websocket connection to the hub
func (c *Client) ReadPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	if err := c.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		log.Printf("Error setting read deadline: %v", err)
		return
	}
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetPongHandler(func(string) error {
		if err := c.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
			log.Printf("Error setting read deadline in pong handler: %v", err)
		}
		return nil
	})

		for {
			_, _, err := c.conn.ReadMessage()
			if err != nil {
				if gorillaWS.IsUnexpectedCloseError(err, gorillaWS.CloseGoingAway, gorillaWS.CloseAbnormalClosure) {
					log.Printf("WebSocket error: %v", err)
				}
				break
			}
		}
}

// WritePump pumps messages from the hub to the websocket connection
func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				log.Printf("Error setting write deadline: %v", err)
				return
			}
			if !ok {
				if err := c.conn.WriteMessage(gorillaWS.CloseMessage, []byte{}); err != nil {
					log.Printf("Error writing close message: %v", err)
				}
				return
			}

			w, err := c.conn.NextWriter(gorillaWS.TextMessage)
			if err != nil {
				return
			}
			if _, err := w.Write(message); err != nil {
				log.Printf("Error writing message: %v", err)
				w.Close()
				return
			}

			n := len(c.send)
			for i := 0; i < n; i++ {
				if _, err := w.Write([]byte{'\n'}); err != nil {
					log.Printf("Error writing newline: %v", err)
					w.Close()
					return
				}
				if _, err := w.Write(<-c.send); err != nil {
					log.Printf("Error writing queued message: %v", err)
					w.Close()
					return
				}
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				log.Printf("Error setting write deadline for ping: %v", err)
				return
			}
			if err := c.conn.WriteMessage(gorillaWS.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// Upgrade upgrades HTTP connection to WebSocket using the provided upgrader
func Upgrade(upgrader gorillaWS.Upgrader, w http.ResponseWriter, r *http.Request, responseHeader http.Header) (*gorillaWS.Conn, error) {
	return upgrader.Upgrade(w, r, responseHeader)
}


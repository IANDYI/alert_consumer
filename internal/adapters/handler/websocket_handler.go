package handler

import (
	"log"
	"net/http"
	"strings"

	gorillaWS "github.com/gorilla/websocket"

	"github.com/IANDYI/alert-consumer/internal/adapters/middleware"
	"github.com/IANDYI/alert-consumer/internal/adapters/websocket"
)

// WebSocketHandler handles WebSocket connections
type WebSocketHandler struct {
	hub            *websocket.Hub
	authMiddleware *middleware.AuthMiddleware
	upgrader       gorillaWS.Upgrader
}

// NewWebSocketHandler creates a new WebSocket handler
func NewWebSocketHandler(hub *websocket.Hub, authMiddleware *middleware.AuthMiddleware, allowedOrigins []string) *WebSocketHandler {
	upgrader := websocket.NewUpgrader(allowedOrigins)
	return &WebSocketHandler{
		hub:            hub,
		authMiddleware: authMiddleware,
		upgrader:       upgrader,
	}
}

// HandleWebSocket handles WebSocket upgrade and connection
func (h *WebSocketHandler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	var userID, role, email, firstName, lastName string
	var ok bool

	authHeader := r.Header.Get("Authorization")
	tokenString := ""
	if authHeader != "" {
		tokenString = strings.TrimPrefix(authHeader, "Bearer ")
		if tokenString == authHeader {
			parts := strings.Split(authHeader, " ")
			if len(parts) == 2 && parts[0] == "Bearer" {
				tokenString = parts[1]
			}
		}
	}

	if tokenString == "" {
		tokenString = r.URL.Query().Get("token")
	}

	if tokenString == "" {
		log.Printf("WebSocket connection rejected: missing token")
		http.Error(w, "unauthorized: missing token", http.StatusUnauthorized)
		return
	}

	userID, role, email, firstName, lastName, ok = h.validateToken(tokenString)

	if !ok || userID == "" {
		log.Printf("WebSocket connection rejected: invalid token")
		http.Error(w, "unauthorized: invalid token", http.StatusUnauthorized)
		return
	}

	// Upgrade connection
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	userName := firstName + " " + lastName
	if userName == " " {
		userName = userID
	}
	if email == "" {
		email = "unknown"
	}

	client := &websocket.Client{
		hub:       h.hub,
		conn:      conn,
		send:      make(chan []byte, 256),
		userID:    userID,
		userRole:  role,
		userEmail: email,
		userName:  userName,
	}

	h.hub.register <- client

	WebSocketConnections.WithLabelValues(strings.ToLower(role)).Inc()

	go client.writePump()
	go client.readPump()
}

func (h *WebSocketHandler) validateToken(tokenString string) (userID, role, email, firstName, lastName string, ok bool) {
	if h.authMiddleware == nil {
		return "", "", "", "", "", false
	}

	claims, _, err := h.authMiddleware.GetClaimsFromCacheOrParse(tokenString)
	if err != nil {
		log.Printf("Token validation failed: %v", err)
		return "", "", "", "", "", false
	}

	userIDClaim, ok := claims["sub"].(string)
	if !ok || userIDClaim == "" {
		log.Printf("Missing or invalid 'sub' claim")
		return "", "", "", "", "", false
	}

	roleClaim, ok := claims["role"].(string)
	if !ok || roleClaim == "" {
		log.Printf("Missing or invalid 'role' claim")
		return "", "", "", "", "", false
	}

	email, _ = claims["email"].(string)
	firstName, _ = claims["first_name"].(string)
	lastName, _ = claims["last_name"].(string)

	return userIDClaim, roleClaim, email, firstName, lastName, true
}


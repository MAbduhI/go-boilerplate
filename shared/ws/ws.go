package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/MAbduhI/go-boilerplate/shared/logger"
	"github.com/MAbduhI/go-boilerplate/shared/security"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 512 * 1024 // 512KB
)

var defaultUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow cross-origin; customize per environment
	},
}

// Client represents a connected websocket peer.
type Client struct {
	ID       string
	Hub      *Hub
	conn     *websocket.Conn
	send     chan []byte
	metadata sync.Map
}

// Set stores arbitrary context on client session.
func (c *Client) Set(key string, val any) {
	c.metadata.Store(key, val)
}

// Get retrieves context on client session.
func (c *Client) Get(key string) (any, bool) {
	return c.metadata.Load(key)
}

// Hub maintains the set of active clients and broadcasts messages.
type Hub struct {
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	mu         sync.RWMutex
}

// NewHub creates a new WebSocket Hub.
func NewHub() *Hub {
	return &Hub{
		broadcast:  make(chan []byte, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		clients:    make(map[*Client]bool),
	}
}

// Run starts the event loop for the Hub.
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			h.mu.Lock()
			for client := range h.clients {
				close(client.send)
				_ = client.conn.Close()
				delete(h.clients, client)
			}
			h.mu.Unlock()
			return
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
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
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Broadcast emits message to all connected clients.
func (h *Hub) Broadcast(msg any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	h.broadcast <- b
	return nil
}

// Count returns number of active clients.
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// UpgradeWithPKCE authenticates WS connection using PKCE parameters (code_verifier & challenge state).
func UpgradeWithPKCE(
	c *gin.Context,
	hub *Hub,
	expectedChallenge string,
	method string,
	onMessage func(client *Client, msgType int, payload []byte),
) (*Client, error) {
	verifier := c.Query("code_verifier")
	if verifier == "" {
		verifier = c.GetHeader("X-Code-Verifier")
	}

	if err := security.VerifyPKCE(verifier, expectedChallenge, method); err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error": gin.H{
				"code":   "PKCE_VERIFICATION_FAILED",
				"detail": err.Error(),
			},
		})
		return nil, fmt.Errorf("ws: pkce verification failed: %w", err)
	}

	conn, err := defaultUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return nil, err
	}

	client := &Client{
		ID:   c.GetString("request_id"),
		Hub:  hub,
		conn: conn,
		send: make(chan []byte, 256),
	}

	hub.register <- client

	go client.writePump(c.Request.Context())
	go client.readPump(onMessage)

	return client, nil
}

func (c *Client) readPump(onMessage func(client *Client, msgType int, payload []byte)) {
	defer func() {
		c.Hub.unregister <- c
		_ = c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		msgType, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				logger.Module("ws").Warn("ws read error", slog.String("error", err.Error()))
			}
			break
		}
		if onMessage != nil {
			onMessage(c, msgType, message)
		}
	}
}

func (c *Client) writePump(ctx context.Context) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			_, _ = w.Write(message)

			// Add queued messages to current websocket frame.
			for len(c.send) > 0 {
				_, _ = w.Write([]byte("\n"))
				_, _ = w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

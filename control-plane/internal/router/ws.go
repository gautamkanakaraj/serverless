package router

import (
	"log"
	"net/http"
	"github.com/gorilla/websocket"
)

// Configure the Upgrader to upgrade HTTP to WebSockets
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow local testing across ports
	},
}

// A simple global list of active dashboard connections
var activeClients = make(map[*websocket.Conn]bool)

// WsHandler upgrades the connection and registers the client
func WsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("WebSocket Upgrade Error:", err)
		return
	}
	
	activeClients[conn] = true
	log.Println("New Dashboard Terminal Connected via WebSocket!")

	// Listen for client disconnects
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			delete(activeClients, conn)
			conn.Close()
			break
		}
	}
}

// BroadcastLog is called by your execution sandboxes to send real-time data
func BroadcastLog(message string) {
	for client := range activeClients {
		err := client.WriteMessage(websocket.TextMessage, []byte(message))
		if err != nil {
			client.Close()
			delete(activeClients, client)
		}
	}
}
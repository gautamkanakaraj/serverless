package router
 
import (
	"log"
	"net/http"
	"sync"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
)
 
// Configure the Upgrader to upgrade HTTP to WebSockets
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow local testing across ports
	},
}
 
// Active dashboard connections partitioned by userID
var (
	activeClients   = make(map[string]map[*websocket.Conn]bool)
	activeClientsMu sync.Mutex
)
 
// WsHandler upgrades the connection and registers the client under their authenticated userID
func WsHandler(w http.ResponseWriter, r *http.Request) {
	// 1. Authenticate using JWT session cookie
	cookie, err := r.Cookie("session_token")
	if err != nil {
		log.Printf("[WS Auth] Rejecting websocket: missing session token cookie")
		http.Error(w, "Unauthorized: Session cookie missing", http.StatusUnauthorized)
		return
	}

	token, err := jwt.Parse(cookie.Value, func(token *jwt.Token) (interface{}, error) {
		return getJWTSecret(), nil
	})

	if err != nil || !token.Valid {
		log.Printf("[WS Auth] Rejecting websocket: invalid session token: %v", err)
		http.Error(w, "Unauthorized: Invalid session token", http.StatusUnauthorized)
		return
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		http.Error(w, "Unauthorized: Invalid claims", http.StatusUnauthorized)
		return
	}

	userID, ok := claims["user_id"].(string)
	if !ok || userID == "" {
		http.Error(w, "Unauthorized: User context missing from token", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("WebSocket Upgrade Error:", err)
		return
	}
	
	activeClientsMu.Lock()
	if activeClients[userID] == nil {
		activeClients[userID] = make(map[*websocket.Conn]bool)
	}
	activeClients[userID][conn] = true
	activeClientsMu.Unlock()
	log.Printf("New Dashboard Terminal Connected via WebSocket for user %s!", userID)
 
	// Listen for client disconnects
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			activeClientsMu.Lock()
			if clients, ok := activeClients[userID]; ok {
				delete(clients, conn)
				if len(clients) == 0 {
					delete(activeClients, userID)
				}
			}
			activeClientsMu.Unlock()
			conn.Close()
			break
		}
	}
}
 
// BroadcastLog is called by execution sandboxes to send real-time data to a specific user
func BroadcastLog(userID string, message string) {
	activeClientsMu.Lock()
	defer activeClientsMu.Unlock()

	clients, ok := activeClients[userID]
	if !ok {
		return
	}

	for client := range clients {
		err := client.WriteMessage(websocket.TextMessage, []byte(message))
		if err != nil {
			client.Close()
			delete(clients, client)
		}
	}
	if len(clients) == 0 {
		delete(activeClients, userID)
	}
}
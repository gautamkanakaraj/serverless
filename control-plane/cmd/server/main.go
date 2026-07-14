package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"serverless/control-plane/internal/db"
	"serverless/control-plane/internal/router"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

type DeployRequest struct {
	UserID      string `json:"user_id"`
	CodeContent string `json:"code_content"`
	Language    string `json:"language"`
}

type DeployResponse struct {
	FunctionID string `json:"function_id"`
	PublicURL  string `json:"public_url"`
	Message    string `json:"message"`
}

func deployHandler(w http.ResponseWriter, r *http.Request) {
	if router.SetupCORS(w, r) {
		return
	}

	// Block anything that isn't a POST request
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req DeployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	lang := req.Language
	if lang == "" {
		lang = "javascript"
	}

	functionID := uuid.New().String()
	publicURL := fmt.Sprintf("/user/code/%s", functionID)

	userCtx, ok := r.Context().Value(router.UserContextKey).(*router.UserContext)
	var userID string
	if ok && userCtx != nil {
		userID = userCtx.UserID
	} else {
		userID = req.UserID
	}

	// Retrieve dedicated user database pool
	userDB, err := db.GetUserDB(userID)
	if err != nil {
		log.Printf("Failed to retrieve user database pool: %v", err)
		http.Error(w, "User database isolation pool unavailable", http.StatusInternalServerError)
		return
	}

	if db.MockMode {
		db.MockMu.Lock()
		// Save metadata registry
		db.MockFunctions[functionID] = db.MockFunctionRecord{
			ID:          functionID,
			UserID:      userID,
			CodeContent: "REGISTRY_ONLY",
			Language:    lang,
			PublicURL:   publicURL,
			CreatedAt:   time.Now(),
		}
		// Save to isolated database
		if db.MockIsolatedFunctions[userID] == nil {
			db.MockIsolatedFunctions[userID] = make(map[string]db.MockFunctionRecord)
		}
		db.MockIsolatedFunctions[userID][functionID] = db.MockFunctionRecord{
			ID:          functionID,
			UserID:      userID,
			CodeContent: req.CodeContent,
			Language:    lang,
			PublicURL:   publicURL,
			CreatedAt:   time.Now(),
		}
		db.MockMu.Unlock()
		log.Printf("[Mock DB] Deployed function %s to user isolated database for user %s", functionID, userID)
	} else {
		// 1. Insert metadata registry into Master DB
		masterQuery := `INSERT INTO functions (id, user_id, code_content, language, public_url, created_at) 
		                VALUES ($1, $2, $3, $4, $5, $6)`
		_, err = db.DB.Exec(masterQuery, functionID, userID, "REGISTRY_ONLY", lang, publicURL, time.Now())
		if err != nil {
			log.Printf("Failed to insert function metadata in master DB: %v", err)
			http.Error(w, "Failed to register function metadata", http.StatusInternalServerError)
			return
		}

		// 2. Insert real function content into isolated User DB
		userQuery := `INSERT INTO functions (id, user_id, code_content, language, public_url, created_at) 
		              VALUES ($1, $2, $3, $4, $5, $6)`
		_, err = userDB.Exec(userQuery, functionID, userID, req.CodeContent, lang, publicURL, time.Now())
		if err != nil {
			log.Printf("Failed to deploy function to user isolated DB: %v", err)
			// Rollback master insertion to keep them in sync
			db.DB.Exec("DELETE FROM functions WHERE id = $1", functionID)
			http.Error(w, "Failed to write deployment payload to isolated database", http.StatusInternalServerError)
			return
		}
		log.Printf("[DB] Function %s successfully written to user %s isolated database", functionID, userID)
	}

	res := DeployResponse{
		FunctionID: functionID,
		PublicURL:  publicURL,
		Message:    "Deployment successful!",
	}

	w.Header().Set("Content-Type", "application/json") // Syntax error fixed here
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(res)
}

func main() {
	godotenv.Load()
	db.InitDB() // inital the neon db

	// Auth API endpoints
	http.HandleFunc("/api/auth/login", router.LoginHandler)
	http.HandleFunc("/api/auth/callback", router.CallbackHandler)
	http.HandleFunc("/api/auth/me", router.MeHandler)
	http.HandleFunc("/api/auth/logout", router.LogoutHandler)

	// Deploy routes (protected)
	http.HandleFunc("/api/deploy", router.AuthenticateMiddleware(deployHandler))
	http.HandleFunc("/user/code/", router.ExecuteHandler)
	http.HandleFunc("/api/ws", router.WsHandler) // live terminal WebSocket endpoint

	// Settings routes
	http.HandleFunc("/api/settings/db", router.AuthenticateMiddleware(router.SaveDBSettingsHandler))

	// Serve the frontend static files
	http.Handle("/", http.FileServer(http.Dir("frontend/src")))

	port := ":8080"
	fmt.Printf("Control Plane running on port %s...\n", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}



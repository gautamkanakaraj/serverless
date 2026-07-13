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
	// 1. Set standard CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type") // avoids corrs error

	// 2. Catch the invisible browser Preflight request
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.WriteHeader(http.StatusOK)
		return
	}

	// 3. Block anything that isn't a POST request
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

	query := `INSERT INTO functions (id, user_id, code_content, language, public_url, created_at) 
              VALUES ($1, $2, $3, $4, $5, $6)`

	_, err := db.DB.Exec(query, functionID, req.UserID, req.CodeContent, lang, publicURL, time.Now())
	if err != nil {
		log.Printf("Failed to insert function: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
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

	http.HandleFunc("/api/deploy", deployHandler)
	http.HandleFunc("/user/code/", router.ExecuteHandler)
	http.HandleFunc("/api/ws", router.WsHandler) // live terminal WebSocket endpoint

	port := ":8080"
	fmt.Printf("Control Plane running on port %s...\n", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}



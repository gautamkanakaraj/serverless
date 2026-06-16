
package router

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"serverless/control-plane/internal/db"
	"serverless/sandbox/engine" // <-- Add our new sandbox engine package
	"github.com/google/uuid"
)

// ExecuteHandler intercepts requests to /user/code/{id}
func ExecuteHandler(w http.ResponseWriter, r *http.Request) {
	// 1. Extract function ID
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 4 {
		http.Error(w, "Invalid function URL", http.StatusBadRequest)
		return
	}
	functionID := pathParts[3]

	// 2. Fetch the code from Neon Postgres
	var codeContent string
	query := `SELECT code_content FROM functions WHERE id = $1`
	err := db.DB.QueryRow(query, functionID).Scan(&codeContent)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Function not found", http.StatusNotFound)
		} else {
			http.Error(w, "Database Error", http.StatusInternalServerError)
		}
		return
	}

	// 3. Execute in the Actual Sandbox!
	// We pass the code to our secure JS Isolate instead of the mock
	executionOutput := engine.ExecuteJS(codeContent)

	// 4. Save Execution Logs to Neon
	logID := uuid.New().String()
	logQuery := `INSERT INTO execution_logs (id, function_id, log_output, timestamp) 
	             VALUES ($1, $2, $3, $4)`
	_, err = db.DB.Exec(logQuery, logID, functionID, executionOutput, time.Now())
	if err != nil {
		log.Printf("Warning: Failed to save log: %v", err)
	}

	// 5. Return Output
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*") // Handle CORS for local testing
	
	json.NewEncoder(w).Encode(map[string]string{
		"status":      "success",
		"function_id": functionID,
		"output":      executionOutput,
	})
}
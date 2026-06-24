package router

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"serverless/control-plane/internal/db"
	"serverless/sandbox/engine"
)

func ExecuteHandler(w http.ResponseWriter, r *http.Request) {
	// 1. Extract the function ID from the URL path (/user/code/{id})
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 4 {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}
	functionID := pathParts[3]

	// 2. Fetch the user's code from Neon Postgres
	var codeContent string
	query := `SELECT code_content FROM functions WHERE id = $1`

	err := db.DB.QueryRow(query, functionID).Scan(&codeContent)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Function not found", http.StatusNotFound)
		} else {
			http.Error(w, "Database error", http.StatusInternalServerError)
		}
		return
	}

	// 3. Send code to the execution sandbox
	output := engine.ExecuteJS(codeContent, BroadcastLog)

	// 4. Return the execution results to the browser
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(output))
}

// Temporary mock engine until we build the Goja sandbox
func simulateSandboxExecution(code string) string {
	time.Sleep(300 * time.Millisecond) // Simulating a fast cold start
	return fmt.Sprintf("Sandbox spun up successfully!\n\nExecuting Code:\n%s\n\n[Simulated Output: 200 OK]", code)
}

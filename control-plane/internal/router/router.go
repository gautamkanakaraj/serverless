package router

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"serverless/control-plane/internal/db"
	"serverless/sandbox/engine"

	"github.com/google/uuid"
)

func ExecuteHandler(w http.ResponseWriter, r *http.Request) {
	// 1. Extract the function ID from the URL path (/user/code/{id})
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 4 {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}
	functionID := pathParts[3]

	// 2. Fetch the user's code and language from Neon Postgres or Mock Store
	var codeContent string
	var language string

	if db.MockMode {
		db.MockMu.RLock()
		rec, exists := db.MockFunctions[functionID]
		db.MockMu.RUnlock()
		if !exists {
			http.Error(w, "Function not found", http.StatusNotFound)
			return
		}
		codeContent = rec.CodeContent
		language = rec.Language
	} else {
		query := `SELECT code_content, language FROM functions WHERE id = $1`
		err := db.DB.QueryRow(query, functionID).Scan(&codeContent, &language)
		if err != nil {
			if err == sql.ErrNoRows {
				http.Error(w, "Function not found", http.StatusNotFound)
			} else {
				http.Error(w, "Database error", http.StatusInternalServerError)
			}
			return
		}
	}

	// 3. Send code to the execution sandbox and measure duration
	startTime := time.Now()
	var output string

	if strings.ToLower(language) == "python" {
		output = engine.ExecutePython(codeContent, BroadcastLog)
	} else {
		output = engine.ExecuteJS(codeContent, BroadcastLog)
	}

	durationMs := int(time.Since(startTime).Milliseconds())

	// 4. Determine status code and error messages
	statusCode := http.StatusOK
	var errMsg sql.NullString

	if strings.HasPrefix(output, "Execution Timeout") {
		statusCode = http.StatusInternalServerError
		errMsg = sql.NullString{String: "Execution Timeout", Valid: true}
	} else if strings.HasPrefix(output, "Runtime Error:") || strings.HasPrefix(output, "Execution Error:") {
		statusCode = http.StatusInternalServerError
		errMsg = sql.NullString{String: output, Valid: true}
	}

	// 5. Write execution log to the database or Mock Store
	logID := uuid.New().String()
	if db.MockMode {
		db.MockMu.Lock()
		errMsgStr := ""
		if errMsg.Valid {
			errMsgStr = errMsg.String
		}
		db.MockLogs[functionID] = append(db.MockLogs[functionID], db.MockLogRecord{
			ID:           logID,
			FunctionID:   functionID,
			LogOutput:    output,
			DurationMs:   durationMs,
			StatusCode:   statusCode,
			ErrorMessage: errMsgStr,
			Timestamp:    time.Now(),
		})
		db.MockMu.Unlock()
		log.Printf("[Mock DB] Recorded execution log for %s (Status: %d, Time: %dms)", functionID, statusCode, durationMs)
	} else {
		logQuery := `INSERT INTO execution_logs (id, function_id, log_output, duration_ms, status_code, error_message, timestamp) 
	                 VALUES ($1, $2, $3, $4, $5, $6, $7)`
		_, dbErr := db.DB.Exec(logQuery, logID, functionID, output, durationMs, statusCode, errMsg, time.Now())
		if dbErr != nil {
			log.Printf("Failed to record execution log to DB: %v", dbErr)
		}
	}

	// 6. Return the execution results to the browser
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(statusCode)
	w.Write([]byte(output))
}

// Temporary mock engine until we build the Goja sandbox
func simulateSandboxExecution(code string) string {
	time.Sleep(300 * time.Millisecond) // Simulating a fast cold start
	return fmt.Sprintf("Sandbox spun up successfully!\n\nExecuting Code:\n%s\n\n[Simulated Output: 200 OK]", code)
}

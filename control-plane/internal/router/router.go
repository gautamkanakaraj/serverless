package router

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
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

	// 2. Fetch the user's code and language from their isolated database pool
	var codeContent string
	var language string
	var userID string

	// Step A: Look up who owns this function from the Master Control Database (metadata registry)
	if db.MockMode {
		db.MockMu.RLock()
		meta, metaExists := db.MockFunctions[functionID]
		db.MockMu.RUnlock()
		if !metaExists {
			http.Error(w, "Function not found in registry", http.StatusNotFound)
			return
		}
		userID = meta.UserID
	} else {
		metaQuery := `SELECT user_id FROM functions WHERE id = $1`
		err := db.DB.QueryRow(metaQuery, functionID).Scan(&userID)
		if err != nil {
			if err == sql.ErrNoRows {
				http.Error(w, "Function not found in registry", http.StatusNotFound)
			} else {
				log.Printf("Failed to look up function ownership: %v", err)
				http.Error(w, "Control plane routing error", http.StatusInternalServerError)
			}
			return
		}
	}

	// Step B: Get the connection pool for this user's isolated database
	userDB, err := db.GetUserDB(userID)
	if err != nil {
		log.Printf("Failed to resolve user isolated database pool: %v", err)
		http.Error(w, "User isolated database pool unavailable", http.StatusInternalServerError)
		return
	}

	// Step C: Load the code content from the user's isolated database
	if db.MockMode {
		db.MockMu.RLock()
		userFuncs, hasFuncs := db.MockIsolatedFunctions[userID]
		var rec db.MockFunctionRecord
		var exists bool
		if hasFuncs {
			rec, exists = userFuncs[functionID]
		}
		db.MockMu.RUnlock()
		if !exists {
			http.Error(w, "Function code not found in isolated database", http.StatusNotFound)
			return
		}
		codeContent = rec.CodeContent
		language = rec.Language
	} else {
		userCodeQuery := `SELECT code_content, language FROM functions WHERE id = $1`
		err = userDB.QueryRow(userCodeQuery, functionID).Scan(&codeContent, &language)
		if err != nil {
			if err == sql.ErrNoRows {
				http.Error(w, "Function code not found in isolated database", http.StatusNotFound)
			} else {
				log.Printf("Isolated database query failed: %v", err)
				http.Error(w, "Failed to load code from isolated database", http.StatusInternalServerError)
			}
			return
		}
	}

	// 3. Send code to the execution sandbox and measure duration
	startTime := time.Now()
	var logs string
	var output string
	var runErr error

	// Create user-partitioned log callback
	logCallback := func(msg string) {
		BroadcastLog(userID, msg)
	}

	if strings.ToLower(language) == "python" {
		output = engine.ExecutePython(codeContent, logCallback)
		logs = output
	} else {
		// Build HTTP event object to pass to handler(event)
		event := map[string]interface{}{
			"method":  r.Method,
			"path":    r.URL.Path,
			"headers": getHeadersMap(r),
			"query":   getQueryParamsMap(r),
			"body":    getBodyString(r),
		}
		logs, output, runErr = engine.ExecuteJS(codeContent, event, logCallback)
	}

	durationMs := int(time.Since(startTime).Milliseconds())

	// 4. Determine status code and error messages
	statusCode := http.StatusOK
	var errMsg sql.NullString

	if runErr != nil {
		statusCode = http.StatusInternalServerError
		errMsg = sql.NullString{String: runErr.Error(), Valid: true}
	} else if strings.HasPrefix(output, "Execution Timeout") {
		statusCode = http.StatusInternalServerError
		errMsg = sql.NullString{String: "Execution Timeout", Valid: true}
	} else if strings.HasPrefix(output, "Runtime Error:") || strings.HasPrefix(output, "Execution Error:") {
		statusCode = http.StatusInternalServerError
		errMsg = sql.NullString{String: output, Valid: true}
	}

	// 5. Write execution log directly to the user's isolated database or Mock Store
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
			LogOutput:    logs,
			DurationMs:   durationMs,
			StatusCode:   statusCode,
			ErrorMessage: errMsgStr,
			Timestamp:    time.Now(),
		})
		db.MockMu.Unlock()
		log.Printf("[Mock DB] Recorded execution log in user %s isolated database for %s (Status: %d, Time: %dms)", userID, functionID, statusCode, durationMs)
	} else {
		logQuery := `INSERT INTO execution_logs (id, function_id, log_output, duration_ms, status_code, error_message, timestamp) 
	                 VALUES ($1, $2, $3, $4, $5, $6, $7)`
		_, dbErr := userDB.Exec(logQuery, logID, functionID, logs, durationMs, statusCode, errMsg, time.Now())
		if dbErr != nil {
			log.Printf("Failed to record execution log to user isolated DB: %v", dbErr)
		}
	}

	// 6. Return the execution results to the browser
	// Check if output is a JSON response struct with status, body, and/or headers
	var jsResp struct {
		Status  int               `json:"status"`
		Body    interface{}       `json:"body"`
		Headers map[string]string `json:"headers"`
	}

	isJSONResponse := false
	trimmedOutput := strings.TrimSpace(output)
	if strings.HasPrefix(trimmedOutput, "{") && strings.HasSuffix(trimmedOutput, "}") {
		if err := json.Unmarshal([]byte(trimmedOutput), &jsResp); err == nil {
			// Require at least Status or Body to be present to treat it as structured response
			if jsResp.Status != 0 || jsResp.Body != nil || jsResp.Headers != nil {
				isJSONResponse = true
			}
		}
	}

	if isJSONResponse {
		// Copy response headers
		for k, v := range jsResp.Headers {
			w.Header().Set(k, v)
		}
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}

		// Set status code
		respStatus := jsResp.Status
		if respStatus == 0 {
			respStatus = statusCode
		}
		w.WriteHeader(respStatus)

		// Output response body
		var bodyBytes []byte
		if jsResp.Body != nil {
			switch b := jsResp.Body.(type) {
			case string:
				bodyBytes = []byte(b)
			default:
				bodyBytes, _ = json.Marshal(b)
			}
		} else {
			bodyBytes = []byte(output)
		}
		w.Write(bodyBytes)
	} else {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(statusCode)
		w.Write([]byte(output))
	}
}

// Helper utilities to parse HTTP requests into execution event structures
func getHeadersMap(r *http.Request) map[string]string {
	m := make(map[string]string)
	for k, v := range r.Header {
		if len(v) > 0 {
			m[k] = v[0]
		}
	}
	return m
}

func getQueryParamsMap(r *http.Request) map[string]string {
	m := make(map[string]string)
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			m[k] = v[0]
		}
	}
	return m
}

func getBodyString(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return ""
	}
	return string(bodyBytes)
}

// Temporary mock engine until we build the Goja sandbox
func simulateSandboxExecution(code string) string {
	time.Sleep(300 * time.Millisecond) // Simulating a fast cold start
	return fmt.Sprintf("Sandbox spun up successfully!\n\nExecuting Code:\n%s\n\n[Simulated Output: 200 OK]", code)
}

package router

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"serverless/control-plane/internal/db"
	"serverless/sandbox/engine"

	"github.com/google/uuid"
	rcron "github.com/robfig/cron/v3"
)

var (
	cronScheduler  *rcron.Cron
	activeCronJobs = make(map[string]rcron.EntryID)
	cronMu         sync.Mutex
)

// InitializeCronScheduler starts the background cron worker and loads existing schedules
func InitializeCronScheduler() {
	cronScheduler = rcron.New()

	if db.MockMode {
		log.Println("[Cron] Starting scheduler in Mock Mode...")
		db.MockMu.RLock()
		for _, fn := range db.MockFunctions {
			if fn.CronExpression != "" {
				registerCronJob(fn.ID, fn.UserID, fn.CodeContent, fn.Language, fn.CronExpression)
			}
		}
		db.MockMu.RUnlock()
	} else {
		log.Println("[Cron] Querying active cron jobs from Master DB...")
		query := `SELECT id, user_id, code_content, language, cron_expression FROM functions WHERE cron_expression IS NOT NULL AND cron_expression != ''`
		rows, err := db.DB.Query(query)
		if err != nil {
			log.Printf("[Cron] Error loading cron functions: %v", err)
		} else {
			defer rows.Close()
			for rows.Next() {
				var id, userID, codeContent, language, cronExpr string
				if err := rows.Scan(&id, &userID, &codeContent, &language, &cronExpr); err == nil {
					registerCronJob(id, userID, codeContent, language, cronExpr)
				} else {
					log.Printf("[Cron] Error scanning row: %v", err)
				}
			}
		}
	}

	cronScheduler.Start()
	log.Println("[Cron] Background task scheduler started successfully.")
}

// RegisterOrUpdateCronJob registers or updates a scheduled task inside the cron engine
func RegisterOrUpdateCronJob(functionID, userID, codeContent, language, cronExpression string) {
	cronMu.Lock()
	defer cronMu.Unlock()

	registerCronJob(functionID, userID, codeContent, language, cronExpression)
}

// Helper to register the job in the cron scheduler (caller must hold lock or during init)
func registerCronJob(functionID, userID, codeContent, language, cronExpression string) {
	// 1. Remove existing schedule if present
	if entryID, exists := activeCronJobs[functionID]; exists {
		cronScheduler.Remove(entryID)
		delete(activeCronJobs, functionID)
	}

	// 2. If cron expression is empty, return
	if cronExpression == "" {
		return
	}

	// 3. Register the new schedule
	entryID, err := cronScheduler.AddFunc(cronExpression, func() {
		executeScheduledFunction(functionID, userID, codeContent, language)
	})
	if err != nil {
		log.Printf("[Cron] Failed to schedule function %s with expression '%s': %v", functionID, cronExpression, err)
		return
	}

	activeCronJobs[functionID] = entryID
	log.Printf("[Cron] Scheduled function %s with expression '%s'", functionID, cronExpression)
}

// RemoveCronJob removes a function from the active schedule
func RemoveCronJob(functionID string) {
	cronMu.Lock()
	defer cronMu.Unlock()

	if entryID, exists := activeCronJobs[functionID]; exists {
		cronScheduler.Remove(entryID)
		delete(activeCronJobs, functionID)
		log.Printf("[Cron] Removed function %s from active schedule", functionID)
	}
}

// executeScheduledFunction runs the serverless function inside a sandboxed environment and logs results
func executeScheduledFunction(functionID, userID, codeContent, language string) {
	log.Printf("[Cron Engine] Triggering scheduled execution for function %s...", functionID)
	startTime := time.Now()

	// Create live log streaming callback
	logCallback := func(msg string) {
		BroadcastLog(userID, "[Cron Run] "+msg)
	}

	BroadcastLog(userID, fmt.Sprintf("[Cron Run] ⏰ Starting scheduled execution for function %s...", functionID))

	// Resolve the user database pool if not running in mock mode
	var userDB *sql.DB
	var err error
	if !db.MockMode {
		userDB, err = db.GetUserDB(userID)
		if err != nil {
			log.Printf("[Cron Engine] Error resolving DB pool for user %s: %v", userID, err)
			BroadcastLog(userID, fmt.Sprintf("[Cron Run] ❌ Execution failed: user database pool unavailable: %v", err))
			return
		}
	}

	var logs string
	var output string
	var runErr error

	if strings.ToLower(language) == "python" {
		output = engine.ExecutePython(codeContent, logCallback)
		logs = output
	} else {
		// Mock HTTP event parameters for cron runs
		event := map[string]interface{}{
			"method":  "SCHEDULED",
			"path":    "/cron/trigger",
			"headers": map[string]string{"User-Agent": "Mini-Lambda-Cron-Scheduler"},
			"query":   map[string]string{},
			"body":    "",
		}
		logs, output, runErr = engine.ExecuteJS(codeContent, event, logCallback)
	}

	durationMs := int(time.Since(startTime).Milliseconds())

	// Determine status and error message
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

	BroadcastLog(userID, fmt.Sprintf("[Cron Run] Finished in %dms (Status: %d)", durationMs, statusCode))

	// Save log record
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
			LogOutput:    "[Cron Run]\n" + logs,
			DurationMs:   durationMs,
			StatusCode:   statusCode,
			ErrorMessage: errMsgStr,
			Timestamp:    time.Now(),
		})
		db.MockMu.Unlock()
	} else {
		logQuery := `INSERT INTO execution_logs (id, function_id, log_output, duration_ms, status_code, error_message, timestamp) 
	                 VALUES ($1, $2, $3, $4, $5, $6, $7)`
		_, dbErr := userDB.Exec(logQuery, logID, functionID, "[Cron Run]\n"+logs, durationMs, statusCode, errMsg, time.Now())
		if dbErr != nil {
			log.Printf("[Cron Engine] Failed to record execution log to user isolated DB: %v", dbErr)
		}
	}
}

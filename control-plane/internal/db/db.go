package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

var DB *sql.DB

// Mock Store for Offline/Isolated Testing Fallback
type MockUserRecord struct {
	ID                 string
	Email              string
	GoogleID           string
	DedicatedDBConnStr string
}

var (
	MockMode      bool
	MockFunctions = make(map[string]MockFunctionRecord)
	MockLogs      = make(map[string][]MockLogRecord)
	MockUsers     = make(map[string]MockUserRecord) // Key is either ID or GoogleID
	MockMu        sync.RWMutex
)

type MockFunctionRecord struct {
	ID          string
	UserID      string
	CodeContent string
	Language    string
	PublicURL   string
	CreatedAt   time.Time
}

type MockLogRecord struct {
	ID           string
	FunctionID   string
	LogOutput    string
	DurationMs   int
	StatusCode   int
	ErrorMessage string
	Timestamp    time.Time
}

func InitDB() {
	connStr := os.Getenv("NEON_DB_URL") 
	if connStr == "" {
		log.Println("NEON_DB_URL is not set. Enabling offline mock mode.")
		MockMode = true
		return
	}

	var err error
	DB, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Printf("Failed to open database connection: %v. Enabling offline mock mode.", err)
		MockMode = true
		return
	}

	// In isolated agent containers, external DB queries are blocked. We retry quickly then fallback.
	maxRetries := 3
	for i := 1; i <= maxRetries; i++ {
		err = DB.Ping()
		if err == nil {
			break
		}
		log.Printf("[DB Connection] Attempt %d/%d failed: %v. Retrying...", i, maxRetries, err)
		if i < maxRetries {
			time.Sleep(1 * time.Second)
		}
	}

	if err != nil {
		log.Printf("⚠️ WARNING: Neon DB unreachable. Falling back to IN-MEMORY MOCK DATABASE for offline testing.")
		MockMode = true
	} else {
		fmt.Println("Successfully connected to Neon Postgres!")
	}

	if MockMode {
		MockMu.Lock()
		MockUsers["123e4567-e89b-12d3-a456-426614174000"] = MockUserRecord{
			ID:    "123e4567-e89b-12d3-a456-426614174000",
			Email: "test@minilambda.com",
		}
		MockMu.Unlock()
	}
}
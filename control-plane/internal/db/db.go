package db

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
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
	MockMode              bool
	MockFunctions         = make(map[string]MockFunctionRecord)
	MockIsolatedFunctions = make(map[string]map[string]MockFunctionRecord) // Key is userID
	MockLogs              = make(map[string][]MockLogRecord)
	MockUsers             = make(map[string]MockUserRecord) // Key is either ID or GoogleID
	MockMu                sync.RWMutex
)

type MockFunctionRecord struct {
	ID             string
	UserID         string
	CodeContent    string
	Language       string
	PublicURL      string
	CronExpression string
	CreatedAt      time.Time
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

	// Neon databases automatically sleep after inactivity. A cold start can take 5-10 seconds.
	// We retry with a 2-second sleep up to 15 times (30 seconds total) to ensure the database can wake up.
	maxRetries := 15
	for i := 1; i <= maxRetries; i++ {
		err = DB.Ping()
		if err == nil {
			break
		}
		log.Printf("[DB Connection] Attempt %d/%d failed: %v. The database might be sleeping (cold start). Retrying in 2 seconds...", i, maxRetries, err)
		if i < maxRetries {
			time.Sleep(2 * time.Second)
		}
	}

	if err != nil {
		log.Printf("⚠️ WARNING: Neon DB unreachable after 30 seconds. Falling back to IN-MEMORY MOCK DATABASE for offline testing.")
		MockMode = true
	} else {
		log.Println("🎉 Successfully connected to Neon Postgres!")
	}

}

var (
	UserDBPools = make(map[string]*sql.DB)
	PoolsMu     sync.RWMutex
)

// GetUserDB retrieves the connection pool for a user's isolated database
func GetUserDB(userID string) (*sql.DB, error) {
	PoolsMu.RLock()
	pool, exists := UserDBPools[userID]
	PoolsMu.RUnlock()
	if exists {
		return pool, nil
	}

	PoolsMu.Lock()
	defer PoolsMu.Unlock()

	// Double-check pattern
	pool, exists = UserDBPools[userID]
	if exists {
		return pool, nil
	}

	var connStr sql.NullString
	if MockMode {
		MockMu.RLock()
		u, ok := MockUsers[userID]
		MockMu.RUnlock()
		if !ok || u.DedicatedDBConnStr == "" {
			return nil, fmt.Errorf("user %s connection string not found in mock database", userID)
		}
		connStr = sql.NullString{String: u.DedicatedDBConnStr, Valid: true}
	} else {
		query := `SELECT dedicated_db_conn_str FROM users WHERE id = $1`
		err := DB.QueryRow(query, userID).Scan(&connStr)
		if err != nil {
			return nil, fmt.Errorf("failed to look up connection string for user %s: %w", userID, err)
		}
	}

	if !connStr.Valid || connStr.String == "" {
		return nil, fmt.Errorf("user %s connection string is empty or invalid", userID)
	}

	if MockMode {
		UserDBPools[userID] = DB
		return DB, nil
	}

	// Decrypt the stored connection string using AES-GCM
	plainConnStr, err := DecryptConnectionString(connStr.String)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt connection string for user %s: %w", userID, err)
	}

	// Open connection pool
	userDB, err := sql.Open("postgres", plainConnStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database pool for user %s: %w", userID, err)
	}

	// Configure pool settings
	userDB.SetMaxOpenConns(10)
	userDB.SetMaxIdleConns(2)
	userDB.SetConnMaxLifetime(5 * time.Minute)

	UserDBPools[userID] = userDB
	return userDB, nil
}

func getEncryptionKey() []byte {
	key := os.Getenv("DB_ENCRYPTION_KEY")
	if len(key) < 32 {
		// Fallback for mock/local development (must be exactly 32 bytes)
		return []byte("a-very-secure-32-byte-long-key-!")
	}
	return []byte(key[:32])
}

// EncryptConnectionString encrypts a plain-text database connection string using AES-GCM
func EncryptConnectionString(plainText string) (string, error) {
	block, err := aes.NewCipher(getEncryptionKey())
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	cipherText := gcm.Seal(nonce, nonce, []byte(plainText), nil)
	return hex.EncodeToString(cipherText), nil
}

// DecryptConnectionString decrypts an AES-GCM encrypted database connection string
func DecryptConnectionString(cipherTextHex string) (string, error) {
	cipherText, err := hex.DecodeString(cipherTextHex)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(getEncryptionKey())
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(cipherText) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, actualCipherText := cipherText[:nonceSize], cipherText[nonceSize:]
	plainText, err := gcm.Open(nil, nonce, actualCipherText, nil)
	if err != nil {
		return "", err
	}

	return string(plainText), nil
}
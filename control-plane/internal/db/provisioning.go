package db

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

type NeonDBResponse struct {
	Database struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	} `json:"database"`
}

func readSchemaFile() (string, error) {
	paths := []string{
		"db/schema.sql",
		"../db/schema.sql",
		"../../db/schema.sql",
		"/home/gautam-kanakaraj/Documents/mini-desktop/serverless/db/schema.sql",
	}
	for _, p := range paths {
		bytes, err := os.ReadFile(p)
		if err == nil {
			return string(bytes), nil
		}
	}
	return "", fmt.Errorf("schema file schema.sql not found in search paths")
}

// ProvisionUserDatabase programmatically creates a database in Neon and runs migrations
func ProvisionUserDatabase(userID, userEmail string) (string, error) {
	// Sanitize userID for database naming
	sanitizedID := strings.ReplaceAll(userID, "-", "")
	dbName := fmt.Sprintf("db_user_%s", sanitizedID)

	apiKey := os.Getenv("NEON_API_KEY")
	projectID := os.Getenv("NEON_PROJECT_ID")
	masterConnStr := os.Getenv("NEON_DB_URL")

	// Return mock connection string ONLY if the server is in MockMode
	if MockMode {
		log.Printf("[Provisioning] Mocking DB creation for user %s (DB Name: %s)", userEmail, dbName)
		mockConnStr := fmt.Sprintf("postgres://mock-host/user-%s", sanitizedID)
		return mockConnStr, nil
	}

	// In real database mode, enforce having valid Neon credentials
	if apiKey == "" || apiKey == "placeholder-neon-api-key" {
		return "", fmt.Errorf("NEON_API_KEY is not configured in .env")
	}
	if projectID == "" || projectID == "placeholder-project-id" {
		return "", fmt.Errorf("NEON_PROJECT_ID is not configured in .env")
	}

	// Construct HTTP Request to Neon API
	apiURL := fmt.Sprintf("https://api.neon.tech/api/v2/projects/%s/databases", projectID)
	payload := map[string]interface{}{
		"database": map[string]string{
			"name": dbName,
		},
	}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request payload: %w", err)
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return "", fmt.Errorf("failed to create http request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("neon api connection failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		// StatusConflict (409) is returned if database already exists, which we handle gracefully
		var errResp map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errResp)
		return "", fmt.Errorf("neon api returned error status %d: %v", resp.StatusCode, errResp)
	}

	log.Printf("[Provisioning] Neon Database '%s' created (or already existed) for user %s", dbName, userEmail)

	// Construct dynamic connection string based on master NEON_DB_URL
	parsedURL, err := url.Parse(masterConnStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse master database connection string: %w", err)
	}
	parsedURL.Path = "/" + dbName
	dedicatedConnStr := parsedURL.String()

	// Run Schema migrations on the newly created database instance
	err = MigrateUserDatabase(dedicatedConnStr, userID, userEmail)
	if err != nil {
		return "", fmt.Errorf("failed to migrate user isolated database: %w", err)
	}

	return dedicatedConnStr, nil
}

// MigrateUserDatabase runs schema.sql tables against the user's dedicated connection string
func MigrateUserDatabase(connStr, userID, userEmail string) error {
	log.Printf("[Provisioning] Initializing schema migrations on user database...")
	
	dbConn, err := sql.Open("postgres", connStr)
	if err != nil {
		return fmt.Errorf("failed to connect to user database: %w", err)
	}
	defer dbConn.Close()

	// Verify connection
	err = dbConn.Ping()
	if err != nil {
		return fmt.Errorf("failed to ping user database: %w", err)
	}

	// Read schema DDL statements
	schemaSQL, err := readSchemaFile()
	if err != nil {
		return fmt.Errorf("failed to read schema migration: %w", err)
	}

	// Run schema DDL query
	_, err = dbConn.Exec(schemaSQL)
	if err != nil {
		return fmt.Errorf("failed to run database schema setup: %w", err)
	}

	// Insert the user into their isolated database to satisfy foreign keys
	userQuery := `INSERT INTO users (id, email) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`
	_, err = dbConn.Exec(userQuery, userID, userEmail)
	if err != nil {
		return fmt.Errorf("failed to sync user identity into dedicated database: %w", err)
	}

	log.Printf("[Provisioning] Schema migration successfully completed on user database for %s!", userEmail)
	return nil
}

package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
)

var DB *sql.DB

func InitDB() {
	connStr := os.Getenv("NEON_DB_URL") 
	if connStr == "" {
		log.Fatal("NEON_DB_URL environment variable is not set")
	}

	var err error
	DB, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Failed to connect to Neon Postgres: %v", err)
	}

	if err = DB.Ping(); err != nil {
		log.Fatalf("Database unreachable: %v", err)
	}

	fmt.Println("Successfully connected to Neon Postgres!")
}
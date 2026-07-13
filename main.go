package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

func main() {
	// 1. Try to load the .env file (ignores error if it doesn't exist)
	godotenv.Load()

	// 2. Grab the connection string
	connStr := os.Getenv("NEON_DB_URL")
	if connStr == "" {
		log.Fatal("Error: NEON_DB_URL is empty. Check your .env file or export command.")
	}

	// 3. Open the connection
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Failed to prepare database connection: %v", err)
	}
	defer db.Close()

	// 4. Ping the database to actually verify the credentials
	err = db.Ping()
	if err != nil {
		log.Fatalf("Cannot reach the database: %v", err)
	}
	fmt.Println("🎉 Connected to Neon Postgres!")

	// 5. Read db/schema.sql to apply schema migrations
	schemaPath := "db/schema.sql"
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		log.Fatalf("Failed to read schema file: %v", err)
	}

	// 6. Execute SQL statements
	_, err = db.Exec(string(schemaBytes))
	if err != nil {
		log.Fatalf("Failed to initialize database schema: %v", err)
	}

	fmt.Println("🚀 Database schema successfully initialized and verified!")
}

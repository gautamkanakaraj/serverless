package router

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"serverless/control-plane/internal/db"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

var oauthConfig *oauth2.Config

func initOAuthConfig() {
	if oauthConfig != nil {
		return
	}
	redirectURL := os.Getenv("GOOGLE_REDIRECT_URL")
	if redirectURL == "" {
		redirectURL = "http://localhost:8080/api/auth/callback"
	}
	oauthConfig = &oauth2.Config{
		RedirectURL:  redirectURL,
		ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		Scopes:       []string{"https://www.googleapis.com/auth/userinfo.email"},
		Endpoint:     google.Endpoint,
	}
}

func getJWTSecret() []byte {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = "super-secret-mini-lambda-key"
	}
	return []byte(secret)
}



func SetupCORS(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	}

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return true
	}
	return false
}

// LoginHandler redirects the user to Google's consent screen
func LoginHandler(w http.ResponseWriter, r *http.Request) {
	if SetupCORS(w, r) {
		return
	}

	initOAuthConfig()

	if oauthConfig.ClientID == "" || oauthConfig.ClientID == "placeholder-client-id" {
		log.Println("[Auth] Google Client ID is not configured. Please set GOOGLE_CLIENT_ID in your environment.")
		http.Error(w, "Authentication is not configured", http.StatusInternalServerError)
		return
	}

	// Real Google Login Flow
	url := oauthConfig.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

// CallbackHandler processes OAuth response and sets cookie session
func CallbackHandler(w http.ResponseWriter, r *http.Request) {
	if SetupCORS(w, r) {
		return
	}

	initOAuthConfig()

	code := r.FormValue("code")
	if code == "" {
		http.Error(w, "Missing authorization code", http.StatusBadRequest)
		return
	}

	if oauthConfig.ClientID == "" || oauthConfig.ClientID == "placeholder-client-id" {
		log.Println("[Auth] Google Client ID is not configured. Rejecting OAuth callback.")
		http.Error(w, "Authentication is not configured", http.StatusInternalServerError)
		return
	}

	// Real Google Exchange
	token, err := oauthConfig.Exchange(context.Background(), code)
	if err != nil {
		log.Printf("[Auth] Failed to exchange token: %v", err)
		http.Error(w, "OAuth exchange failed", http.StatusInternalServerError)
		return
	}

	// Fetch profile info
	client := oauthConfig.Client(context.Background(), token)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		log.Printf("[Auth] Failed to fetch user info: %v", err)
		http.Error(w, "Failed to get user profile", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	var profile struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		log.Printf("[Auth] Failed to parse profile: %v", err)
		http.Error(w, "Failed to parse profile info", http.StatusInternalServerError)
		return
	}
	googleID := profile.ID
	email := profile.Email
	log.Printf("[Auth] Google Authentication success: user %s", email)

	// Resolve / Upsert user in master DB
	var userID string
	if db.MockMode {
		db.MockMu.Lock()
		// Try to find mock user by Google ID or Email
		var foundID string
		for _, u := range db.MockUsers {
			if u.GoogleID == googleID || u.Email == email {
				foundID = u.ID
				break
			}
		}
		if foundID == "" {
			userID = uuid.New().String()
			db.MockUsers[userID] = db.MockUserRecord{
				ID:       userID,
				Email:    email,
				GoogleID: googleID,
			}
		} else {
			userID = foundID
			// Ensure GoogleID is updated
			rec := db.MockUsers[userID]
			rec.GoogleID = googleID
			db.MockUsers[userID] = rec
		}
		// Also index by GoogleID just in case
		db.MockUsers[googleID] = db.MockUsers[userID]
		db.MockMu.Unlock()
	} else {
		query := `SELECT id, email FROM users WHERE google_id = $1`
		err := db.DB.QueryRow(query, googleID).Scan(&userID, &email)
		if err == sql.ErrNoRows {
			// Find by email next to merge existing test users
			query = `SELECT id FROM users WHERE email = $1`
			err = db.DB.QueryRow(query, email).Scan(&userID)
			if err == sql.ErrNoRows {
				userID = uuid.New().String()
				insertQuery := `INSERT INTO users (id, email, google_id) VALUES ($1, $2, $3)`
				_, err = db.DB.Exec(insertQuery, userID, email, googleID)
				if err != nil {
					log.Printf("[Auth] Failed to insert user: %v", err)
					http.Error(w, "Database error", http.StatusInternalServerError)
					return
				}
			} else if err == nil {
				// Link google_id
				updateQuery := `UPDATE users SET google_id = $1 WHERE id = $2`
				_, err = db.DB.Exec(updateQuery, googleID, userID)
				if err != nil {
					log.Printf("[Auth] Failed to link google_id to user: %v", err)
					http.Error(w, "Database error", http.StatusInternalServerError)
					return
				}
			}
		} else if err != nil {
			log.Printf("[Auth] DB select error: %v", err)
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
	}



	// Sign JWT token
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": userID,
		"email":   email,
		"exp":     time.Now().Add(24 * time.Hour).Unix(),
	})
	tokenString, err := jwtToken.SignedString(getJWTSecret())
	if err != nil {
		log.Printf("[Auth] Failed to sign JWT: %v", err)
		http.Error(w, "Token generation failed", http.StatusInternalServerError)
		return
	}

	// Set HTTP-only Cookie.
	// Secure=true is required in production (HTTPS). In local dev (HTTP) it must be false
	// or the browser will silently drop the cookie.
	isProduction := os.Getenv("APP_ENV") == "production"
	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    tokenString,
		Expires:  time.Now().Add(24 * time.Hour),
		Path:     "/",
		HttpOnly: true,
		Secure:   isProduction,
		SameSite: http.SameSiteLaxMode,
	})

	// Instead of a 302 redirect, serve a tiny HTML page that does a JS navigation.
	// This ensures the browser fully commits the Set-Cookie header from THIS response
	// before the next page's JS (checkSession) runs — eliminating the race condition
	// where the browser hadn't yet stored the cookie before fetch('/api/auth/me') fired.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
  <title>Signing in...</title>
  <style>
    body { margin:0; background:#0b0c10; display:flex; align-items:center; justify-content:center; height:100vh; }
    p { color:#a5b4fc; font-family:sans-serif; font-size:1.1rem; }
  </style>
</head>
<body>
  <p>⚡ Signing you in...</p>
  <script>
    // Cookie is now committed. Navigate to dashboard.
    window.location.replace('/');
  </script>
</body>
</html>`)
}

// MeHandler returns the authenticated user's profile info along with database status
func MeHandler(w http.ResponseWriter, r *http.Request) {
	if SetupCORS(w, r) {
		return
	}

	cookie, err := r.Cookie("session_token")
	if err != nil {
		log.Printf("[Auth] MeHandler failed: missing session_token cookie: %v", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	token, err := jwt.Parse(cookie.Value, func(token *jwt.Token) (interface{}, error) {
		return getJWTSecret(), nil
	})

	if err != nil || !token.Valid {
		log.Printf("[Auth] MeHandler failed: invalid token: %v", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	userID := claims["user_id"].(string)
	email := claims["email"].(string)

	hasDB := false
	if db.MockMode {
		db.MockMu.RLock()
		u, ok := db.MockUsers[userID]
		if ok && u.DedicatedDBConnStr != "" {
			hasDB = true
		}
		db.MockMu.RUnlock()
	} else {
		var connStr sql.NullString
		query := `SELECT dedicated_db_conn_str FROM users WHERE id = $1`
		err = db.DB.QueryRow(query, userID).Scan(&connStr)
		if err == nil && connStr.Valid && connStr.String != "" {
			hasDB = true
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"user_id": userID,
		"email":   email,
		"has_db":  hasDB,
	})
}

// AutoProvisionDBHandler programmatically provisions a Neon database for the user, encrypts, and saves it
func AutoProvisionDBHandler(w http.ResponseWriter, r *http.Request) {
	if SetupCORS(w, r) {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userCtx, ok := r.Context().Value(UserContextKey).(*UserContext)
	if !ok || userCtx == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	log.Printf("[Provisioning] Auto-provisioning database requested by %s", userCtx.Email)

	// Call Neon provisioning module
	connStr, err := db.ProvisionUserDatabase(userCtx.UserID, userCtx.Email)
	if err != nil {
		log.Printf("Failed to auto-provision database for user %s: %v", userCtx.Email, err)
		http.Error(w, fmt.Sprintf("Auto-provisioning failed: %v", err), http.StatusInternalServerError)
		return
	}

	if db.MockMode {
		db.MockMu.Lock()
		u, ok := db.MockUsers[userCtx.UserID]
		if ok {
			u.DedicatedDBConnStr = connStr
			db.MockUsers[userCtx.UserID] = u
		}
		db.MockMu.Unlock()
		log.Printf("[Mock Mode] Auto-provisioned mock database for user %s: %s", userCtx.Email, connStr)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Database auto-provisioned successfully (Mock Mode)",
			"connection_string": connStr,
		})
		return
	}

	// Real DB Mode:
	// 3. Encrypt the connection string
	encryptedConnStr, err := db.EncryptConnectionString(connStr)
	if err != nil {
		log.Printf("Failed to encrypt connection string for %s: %v", userCtx.Email, err)
		http.Error(w, "Internal security encryption error", http.StatusInternalServerError)
		return
	}

	// 4. Save connection string in master DB
	saveQuery := `UPDATE users SET dedicated_db_conn_str = $1 WHERE id = $2`
	_, err = db.DB.Exec(saveQuery, encryptedConnStr, userCtx.UserID)
	if err != nil {
		log.Printf("Failed to save user DB config in master DB: %v", err)
		http.Error(w, "Database save error", http.StatusInternalServerError)
		return
	}

	// 5. Invalidate cached pool in registry
	db.PoolsMu.Lock()
	if pool, exists := db.UserDBPools[userCtx.UserID]; exists {
		pool.Close()
		delete(db.UserDBPools, userCtx.UserID)
	}
	db.PoolsMu.Unlock()

	log.Printf("Successfully auto-provisioned and registered database for user %s", userCtx.Email)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Database successfully auto-provisioned and bootstrapped!",
		"connection_string": connStr,
	})
}

type DBSettingsRequest struct {
	ConnectionString string `json:"connection_string"`
}

// SaveDBSettingsHandler verifies, bootstraps, encrypts, and saves the user-provided connection string
func SaveDBSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if SetupCORS(w, r) {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userCtx, ok := r.Context().Value(UserContextKey).(*UserContext)
	if !ok || userCtx == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req DBSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	connStr := req.ConnectionString
	if connStr == "" {
		http.Error(w, "Connection string cannot be empty", http.StatusBadRequest)
		return
	}

	if db.MockMode {
		db.MockMu.Lock()
		u, ok := db.MockUsers[userCtx.UserID]
		if ok {
			u.DedicatedDBConnStr = connStr
			db.MockUsers[userCtx.UserID] = u
		}
		db.MockMu.Unlock()
		log.Printf("[Mock Mode] Saved database connection string for user %s: %s", userCtx.Email, connStr)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"message": "Database settings saved successfully (Mock Mode)"})
		return
	}

	// Real SQL Mode:
	// 1. Verify connection credentials
	testDB, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Printf("Invalid connection string provided by %s: %v", userCtx.Email, err)
		http.Error(w, fmt.Sprintf("Invalid connection string: %v", err), http.StatusBadRequest)
		return
	}
	defer testDB.Close()

	err = testDB.Ping()
	if err != nil {
		log.Printf("Failed to ping database for user %s: %v", userCtx.Email, err)
		http.Error(w, fmt.Sprintf("Failed to connect to database: %v", err), http.StatusBadRequest)
		return
	}

	// 2. Run migrations dynamically to create application tables
	err = bootstrapUserSchema(testDB, userCtx.UserID, userCtx.Email)
	if err != nil {
		log.Printf("Failed to bootstrap user schema for %s: %v", userCtx.Email, err)
		http.Error(w, fmt.Sprintf("Failed to initialize database schema: %v", err), http.StatusInternalServerError)
		return
	}

	// 3. Encrypt the connection string
	encryptedConnStr, err := db.EncryptConnectionString(connStr)
	if err != nil {
		log.Printf("Failed to encrypt connection string for %s: %v", userCtx.Email, err)
		http.Error(w, "Internal security encryption error", http.StatusInternalServerError)
		return
	}

	// 4. Save connection string in master DB
	saveQuery := `UPDATE users SET dedicated_db_conn_str = $1 WHERE id = $2`
	_, err = db.DB.Exec(saveQuery, encryptedConnStr, userCtx.UserID)
	if err != nil {
		log.Printf("Failed to save user DB config in master DB: %v", err)
		http.Error(w, "Database save error", http.StatusInternalServerError)
		return
	}

	// 5. Invalidate cached pool in registry
	db.PoolsMu.Lock()
	if pool, exists := db.UserDBPools[userCtx.UserID]; exists {
		pool.Close()
		delete(db.UserDBPools, userCtx.UserID)
	}
	db.PoolsMu.Unlock()

	log.Printf("Successfully saved and verified database credentials for user %s", userCtx.Email)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Database successfully connected and bootstrapped!"})
}

func bootstrapUserSchema(userDB *sql.DB, userID string, email string) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id UUID PRIMARY KEY,
			email TEXT UNIQUE NOT NULL,
			google_id VARCHAR(255) UNIQUE,
			dedicated_db_conn_str TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS functions (
			id UUID PRIMARY KEY,
			user_id UUID REFERENCES users(id) ON DELETE CASCADE,
			code_content TEXT NOT NULL,
			language TEXT NOT NULL DEFAULT 'javascript',
			public_url TEXT UNIQUE NOT NULL,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_functions_user_id ON functions(user_id)`,
		`CREATE TABLE IF NOT EXISTS execution_logs (
			id UUID PRIMARY KEY,
			function_id UUID REFERENCES functions(id) ON DELETE CASCADE,
			log_output TEXT NOT NULL,
			duration_ms INT,
			status_code INT,
			error_message TEXT,
			timestamp TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_logs_function_id ON execution_logs(function_id)`,
	}

	for _, q := range queries {
		_, err := userDB.Exec(q)
		if err != nil {
			return fmt.Errorf("failed executing bootstrap query: %w", err)
		}
	}

	// Replicate user record in isolated database to satisfy foreign keys
	replicateUserQuery := `INSERT INTO users (id, email) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`
	_, err := userDB.Exec(replicateUserQuery, userID, email)
	if err != nil {
		return fmt.Errorf("failed replicating user record: %w", err)
	}

	return nil
}

// LogoutHandler clears the session cookie
func LogoutHandler(w http.ResponseWriter, r *http.Request) {
	if SetupCORS(w, r) {
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    "",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Logged out successfully"))
}

// AuthenticateMiddleware authenticates requests using the cookie session and injects the context
type contextKey string
const UserContextKey contextKey = "user"

type UserContext struct {
	UserID string
	Email  string
}

func AuthenticateMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if SetupCORS(w, r) {
			return
		}

		cookie, err := r.Cookie("session_token")
		if err != nil {
			http.Error(w, "Unauthorized: Session cookie missing", http.StatusUnauthorized)
			return
		}

		token, err := jwt.Parse(cookie.Value, func(token *jwt.Token) (interface{}, error) {
			return getJWTSecret(), nil
		})

		if err != nil || !token.Valid {
			http.Error(w, "Unauthorized: Invalid session token", http.StatusUnauthorized)
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			http.Error(w, "Unauthorized: Invalid claims", http.StatusUnauthorized)
			return
		}

		userID, ok1 := claims["user_id"].(string)
		email, ok2 := claims["email"].(string)
		if !ok1 || !ok2 {
			http.Error(w, "Unauthorized: User context missing from token", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), UserContextKey, &UserContext{
			UserID: userID,
			Email:  email,
		})

		next(w, r.WithContext(ctx))
	}
}

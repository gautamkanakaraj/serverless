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

func isMockAuthEnabled() bool {
	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	return clientID == "" || clientID == "placeholder-client-id"
}

func SetupCORS(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Fallback to localhost if no origin (or * if not using credentials, but we are)
		origin = "http://localhost:8080"
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return true
	}
	return false
}

// LoginHandler redirects the user to Google's consent screen or triggers mock login if enabled
func LoginHandler(w http.ResponseWriter, r *http.Request) {
	if SetupCORS(w, r) {
		return
	}

	initOAuthConfig()

	if isMockAuthEnabled() {
		log.Println("[Auth] Google Client ID is placeholder or empty. Redirecting to mock login callback.")
		// Directly redirect to the callback with a mock code
		redirectURL := fmt.Sprintf("/api/auth/callback?code=mock_auth_code_123")
		http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
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

	var googleID string
	var email string

	if code == "mock_auth_code_123" || isMockAuthEnabled() {
		// Mock profile data
		googleID = "mock_google_id_123"
		email = "mock-user@minilambda.com"
		log.Printf("[Auth] Mock Authentication success: user %s", email)
	} else {
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
		googleID = profile.ID
		email = profile.Email
		log.Printf("[Auth] Google Authentication success: user %s", email)
	}

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

	// Provision dynamic database
	dedicatedConnStr, err := db.ProvisionUserDatabase(userID, email)
	if err != nil {
		log.Printf("[Auth] Failed to provision dedicated database for user %s: %v", email, err)
		// Log warning and continue so authentication works even if Neon has issues
	} else {
		// Save connection string in master DB / mock record
		if db.MockMode {
			db.MockMu.Lock()
			u := db.MockUsers[userID]
			u.DedicatedDBConnStr = dedicatedConnStr
			db.MockUsers[userID] = u
			db.MockUsers[googleID] = u
			db.MockMu.Unlock()
			log.Printf("[Auth] Saved mock user connection string: %s", dedicatedConnStr)
		} else {
			updateConnStrQuery := `UPDATE users SET dedicated_db_conn_str = $1 WHERE id = $2`
			_, err = db.DB.Exec(updateConnStrQuery, dedicatedConnStr, userID)
			if err != nil {
				log.Printf("[Auth] Failed to save dedicated database connection string: %v", err)
			}
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

	// Set HTTP-only Cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    tokenString,
		Expires:  time.Now().Add(24 * time.Hour),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	// Redirect to frontend dashboard
	http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
}

// MeHandler returns the authenticated user's profile info
func MeHandler(w http.ResponseWriter, r *http.Request) {
	if SetupCORS(w, r) {
		return
	}

	cookie, err := r.Cookie("session_token")
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	token, err := jwt.Parse(cookie.Value, func(token *jwt.Token) (interface{}, error) {
		return getJWTSecret(), nil
	})

	if err != nil || !token.Valid {
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"user_id": userID,
		"email":   email,
	})
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

package router

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"serverless/control-plane/internal/db"
)

func TestSaveDBSettingsHandler(t *testing.T) {
	// Enable Mock Mode for testing
	db.MockMode = true
	
	// Create mock user
	userID := "test-user-uuid"
	db.MockUsers[userID] = db.MockUserRecord{
		ID:    userID,
		Email: "test@minilambda.com",
	}

	// 1. Create a request with connection string payload
	reqBody, _ := json.Marshal(DBSettingsRequest{
		ConnectionString: "postgres://mock-host/user-db",
	})
	req, err := http.NewRequest("POST", "/api/settings/db", bytes.NewBuffer(reqBody))
	if err != nil {
		t.Fatal(err)
	}

	// Inject auth context using the context keys
	ctx := context.WithValue(req.Context(), UserContextKey, &UserContext{
		UserID: userID,
		Email:  "test@minilambda.com",
	})
	req = req.WithContext(ctx)

	// Create ResponseRecorder
	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(SaveDBSettingsHandler)

	// Invoke handler
	handler.ServeHTTP(rr, req)

	// Verify response code
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	// Verify response body
	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response body: %v", err)
	}

	if resp["message"] != "Database settings saved successfully (Mock Mode)" {
		t.Errorf("Unexpected response message: got %q", resp["message"])
	}

	// Verify connection string saved in Mock Users map
	db.MockMu.RLock()
	savedConnStr := db.MockUsers[userID].DedicatedDBConnStr
	db.MockMu.RUnlock()

	if savedConnStr != "postgres://mock-host/user-db" {
		t.Errorf("Connection string not saved: got %q", savedConnStr)
	}
}

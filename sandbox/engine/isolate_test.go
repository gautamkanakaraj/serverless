package engine

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExecuteJSWithFetch(t *testing.T) {
	// 1. Create a local mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"received": true, "method": "POST"}`))
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Hello from local test server!"))
	}))
	defer server.Close()

	// 2. JavaScript code utilizing the fetch() API
	jsCode := fmt.Sprintf(`
		function handler(event) {
			console.log("Calling fetch...");
			var res = fetch("%s", {
				method: "POST",
				body: "some body"
			});
			console.log("Status code: " + res.status);
			console.log("Headers content-type: " + res.headers["Content-Type"]);
			var data = res.json();
			console.log("Response body received: " + data.received);
			return "SUCCESS";
		}
	`, server.URL)

	// 3. Execute the JS code in the sandbox
	event := map[string]interface{}{}
	var logs []string
	logCallback := func(log string) {
		logs = append(logs, log)
	}

	_, result, err := ExecuteJS(jsCode, event, logCallback)
	if err != nil {
		t.Fatalf("Failed to execute JS with fetch: %v", err)
	}

	if result != "SUCCESS" {
		t.Errorf("Expected result 'SUCCESS', got: %s", result)
	}

	// 4. Verify stdout/console logs
	logStr := strings.Join(logs, "\n")
	expectedLogs := []string{
		"Calling fetch...",
		"Status code: 200",
		"Headers content-type: application/json",
		"Response body received: true",
	}

	for _, expected := range expectedLogs {
		if !strings.Contains(logStr, expected) {
			t.Errorf("Expected log containing %q, but logs were:\n%s", expected, logStr)
		}
	}
}

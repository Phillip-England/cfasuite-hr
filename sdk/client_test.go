package sdk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewClientFromEnvRequiresConfiguration(t *testing.T) {
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvAPIKey, "")
	_, err := NewClientFromEnv()
	if err == nil || !strings.Contains(err.Error(), EnvBaseURL) {
		t.Fatalf("expected missing base URL error, got %v", err)
	}

	t.Setenv(EnvBaseURL, "https://hr.example.com")
	_, err = NewClientFromEnv()
	if err == nil || !strings.Contains(err.Error(), EnvAPIKey) {
		t.Fatalf("expected missing API key error, got %v", err)
	}
}

func TestClientEmployeesUsesBearerAuthAndEscapedLocation(t *testing.T) {
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"employees":[{"id":10,"location_id":1,"employee_name":"Blanco, John","employee_number":"12-1083836","job":"Team Member","employee_status":"Active","location_latest_start_date":"2024-10-01","created_at":"2026-06-13T12:00:00Z","updated_at":"2026-06-13T12:00:00Z"}]}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "cfa_test")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	employees, err := client.Employees(context.Background(), "03/394")
	if err != nil {
		t.Fatalf("Employees: %v", err)
	}
	if gotAuth != "Bearer cfa_test" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotPath != "/api/v1/locations/03%2F394/employees" {
		t.Fatalf("path = %q", gotPath)
	}
	if len(employees) != 1 || employees[0].EmployeeNumber != "12-1083836" {
		t.Fatalf("unexpected employees: %#v", employees)
	}
}

func TestClientReturnsAPIErrorMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"valid API token required"}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "bad")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.Locations(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(APIError)
	if !ok {
		t.Fatalf("error type = %T", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized || apiErr.Message != "valid API token required" {
		t.Fatalf("unexpected API error: %#v", apiErr)
	}
}

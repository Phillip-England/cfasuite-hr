package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	EnvBaseURL = "CFASUITE_HR_BASE_URL"
	EnvAPIKey  = "CFASUITE_HR_API_KEY"
)

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

type Location struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Number    string    `json:"number"`
	Email     string    `json:"email,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Employees int       `json:"employee_count,omitempty"`
}

type Employee struct {
	ID                      int64     `json:"id"`
	LocationID              int64     `json:"location_id"`
	EmployeeName            string    `json:"employee_name"`
	EmployeeNumber          string    `json:"employee_number"`
	Job                     string    `json:"job"`
	RoleID                  *int64    `json:"role_id"`
	RoleName                *string   `json:"role_name"`
	DepartmentID            *int64    `json:"department_id"`
	DepartmentName          *string   `json:"department_name"`
	WageRateCents           *int64    `json:"wage_rate_cents"`
	WagePayType             string    `json:"wage_pay_type"`
	ExcludeFromLabor        bool      `json:"exclude_from_labor"`
	EmployeeStatus          string    `json:"employee_status"`
	LocationLatestStartDate string    `json:"location_latest_start_date"`
	BirthDate               *string   `json:"birth_date"`
	ClockInPIN              *string   `json:"clock_in_pin"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

type APIError struct {
	StatusCode int
	Status     string
	Message    string
}

func (e APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("cfasuite-hr returned %s: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("cfasuite-hr returned %s", e.Status)
}

func NewClientFromEnv() (*Client, error) {
	baseURL := strings.TrimSpace(os.Getenv(EnvBaseURL))
	apiKey := strings.TrimSpace(os.Getenv(EnvAPIKey))
	if baseURL == "" {
		return nil, fmt.Errorf("%s is not set; run cfasuite-hr api-key-env -api-key <key> -base-url <url> and load the printed exports", EnvBaseURL)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("%s is not set; run cfasuite-hr api-key-env -api-key <key> -base-url <url> and load the printed exports", EnvAPIKey)
	}
	return NewClient(baseURL, apiKey)
}

func NewClient(baseURL, apiKey string) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	apiKey = strings.TrimSpace(apiKey)
	if baseURL == "" {
		return nil, errors.New("base URL is required")
	}
	if apiKey == "" {
		return nil, errors.New("API key is required")
	}
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}
	return &Client{baseURL: baseURL, apiKey: apiKey, httpClient: http.DefaultClient}, nil
}

func (c *Client) WithHTTPClient(httpClient *http.Client) *Client {
	clone := *c
	if httpClient != nil {
		clone.httpClient = httpClient
	}
	return &clone
}

func (c *Client) Locations(ctx context.Context) ([]Location, error) {
	var payload struct {
		Locations []Location `json:"locations"`
	}
	if err := c.get(ctx, "/api/v1/locations", &payload); err != nil {
		return nil, err
	}
	return payload.Locations, nil
}

func (c *Client) Employees(ctx context.Context, storeNumber string) ([]Employee, error) {
	var payload struct {
		Employees []Employee `json:"employees"`
	}
	if err := c.get(ctx, "/api/v1/locations/"+url.PathEscape(storeNumber)+"/employees", &payload); err != nil {
		return nil, err
	}
	return payload.Employees, nil
}

func (c *Client) Employee(ctx context.Context, storeNumber, employeeNumber string) (Employee, error) {
	var payload struct {
		Employee Employee `json:"employee"`
	}
	if err := c.get(ctx, "/api/v1/locations/"+url.PathEscape(storeNumber)+"/employees/"+url.PathEscape(employeeNumber), &payload); err != nil {
		return Employee{}, err
	}
	return payload.Employee, nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return decodeAPIError(res)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func decodeAPIError(res *http.Response) error {
	var payload struct {
		Error string `json:"error"`
	}
	body := io.LimitReader(res.Body, 4096)
	_ = json.NewDecoder(body).Decode(&payload)
	return APIError{StatusCode: res.StatusCode, Status: res.Status, Message: payload.Error}
}

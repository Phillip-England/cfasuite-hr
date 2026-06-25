package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strings"
)

func newApp(db *sql.DB) (*App, error) {
	secret := os.Getenv("CFASUITE_SESSION_SECRET")
	if secret == "" {
		var err error
		secret, err = getSetting(db, "session_secret")
		if err != nil {
			return nil, err
		}
		if secret == "" {
			secret = randomToken(32)
			if err := setSetting(db, "session_secret", secret); err != nil {
				return nil, err
			}
		}
	}
	username := os.Getenv("CFASUITE_ADMIN_USERNAME")
	password := os.Getenv("CFASUITE_ADMIN_PASSWORD")
	adminHash := ""
	if username == "" || password == "" {
		var err error
		username, err = getSetting(db, "admin_username")
		if err != nil {
			return nil, err
		}
		adminHash, err = getSetting(db, "admin_password_hash")
		if err != nil {
			return nil, err
		}
	}
	dataDir := appDataDir()
	return &App{db: db, dataDir: dataDir, sessionSecret: []byte(secret), adminUsername: username, adminPassword: password, adminHash: adminHash}, nil
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", a.requireAdmin(a.dashboard))
	mux.HandleFunc("GET /login", a.loginPage)
	mux.HandleFunc("POST /login", a.loginPost)
	mux.HandleFunc("POST /logout", a.requireAdmin(a.logoutPost))
	mux.HandleFunc("GET /locations/new", a.requireAdmin(a.locationNew))
	mux.HandleFunc("POST /locations", a.requireAdmin(a.locationCreate))
	mux.HandleFunc("GET /locations/{id}", a.requireAdmin(a.locationShow))
	mux.HandleFunc("GET /locations/{id}/details", a.requireAdmin(a.locationDetails))
	mux.HandleFunc("GET /locations/{id}/pay", a.requireAdmin(a.locationPay))
	mux.HandleFunc("GET /locations/{locationID}/employees/{id}", a.requireAdmin(a.employeeProfile))
	mux.HandleFunc("GET /locations/{id}/calendar", a.requireAdmin(a.locationCalendar))
	mux.HandleFunc("POST /locations/{id}/calendar/productivity-goal", a.requireAdmin(a.locationProductivityGoalUpdate))
	mux.HandleFunc("GET /locations/{id}/calendar/{date}", a.requireAdmin(a.locationCalendarDay))
	mux.HandleFunc("POST /locations/{id}/calendar/{date}/sales", a.requireAdmin(a.locationSalesUpload))
	mux.HandleFunc("POST /locations/{id}/calendar/{date}/labor", a.requireAdmin(a.locationLaborUpload))
	mux.HandleFunc("GET /locations/{id}/sales", a.requireAdmin(a.locationSales))
	mux.HandleFunc("GET /locations/{id}/productivity", a.requireAdmin(a.locationProductivity))
	mux.HandleFunc("GET /locations/{id}/documents", a.requireAdmin(a.locationDocuments))
	mux.HandleFunc("GET /locations/{id}/edit", a.requireAdmin(a.locationEdit))
	mux.HandleFunc("POST /locations/{id}", a.requireAdmin(a.locationUpdate))
	mux.HandleFunc("POST /locations/{id}/delete", a.requireAdmin(a.locationDelete))
	mux.HandleFunc("POST /locations/{id}/upload", a.requireAdmin(a.locationUpload))
	mux.HandleFunc("POST /locations/{id}/birthdays/upload", a.requireAdmin(a.birthdayUpload))
	mux.HandleFunc("POST /locations/{id}/pins/upload", a.requireAdmin(a.pinUpload))
	mux.HandleFunc("POST /locations/{id}/documents/time-punch-wages", a.requireAdmin(a.documentTimePunchWageUpload))
	mux.HandleFunc("POST /locations/{id}/assignments", a.requireAdmin(a.locationAssignmentsUpdate))
	mux.HandleFunc("POST /locations/{id}/roles", a.requireAdmin(a.locationRolesUpdate))
	mux.HandleFunc("GET /locations/{id}/roles", a.requireAdmin(a.rolesPage))
	mux.HandleFunc("POST /locations/{id}/roles/manage", a.requireAdmin(a.roleCreate))
	mux.HandleFunc("POST /locations/{locationID}/roles/{id}", a.requireAdmin(a.roleUpdate))
	mux.HandleFunc("POST /locations/{locationID}/roles/{id}/delete", a.requireAdmin(a.roleDelete))
	mux.HandleFunc("GET /locations/{id}/departments", a.requireAdmin(a.departmentsPage))
	mux.HandleFunc("POST /locations/{id}/departments/manage", a.requireAdmin(a.departmentCreate))
	mux.HandleFunc("POST /locations/{locationID}/departments/{id}", a.requireAdmin(a.departmentUpdate))
	mux.HandleFunc("POST /locations/{locationID}/departments/{id}/delete", a.requireAdmin(a.departmentDelete))
	mux.HandleFunc("GET /locations/{id}/labor", a.requireAdmin(a.laborPage))
	mux.HandleFunc("POST /locations/{id}/labor", a.requireAdmin(a.laborUpload))
	mux.HandleFunc("POST /roles/{id}", a.requireAdmin(a.roleUpdate))
	mux.HandleFunc("POST /roles/{id}/delete", a.requireAdmin(a.roleDelete))
	mux.HandleFunc("POST /departments/{id}", a.requireAdmin(a.departmentUpdate))
	mux.HandleFunc("POST /departments/{id}/delete", a.requireAdmin(a.departmentDelete))
	mux.HandleFunc("GET /tokens", a.requireAdmin(a.tokensPage))
	mux.HandleFunc("POST /tokens", a.requireAdmin(a.tokenCreate))
	mux.HandleFunc("POST /tokens/{id}/delete", a.requireAdmin(a.tokenDelete))
	mux.HandleFunc("GET /docs", a.requireAdmin(a.docsPage))
	mux.HandleFunc("GET /assets/app.css", css)
	mux.HandleFunc("GET /api/v1/locations", a.apiAuth(a.apiLocations))
	mux.HandleFunc("GET /api/v1/locations/{number}/employees/full", a.apiAuth(a.apiEmployees))
	mux.HandleFunc("GET /api/v1/locations/{number}/employees/identity", a.apiAuth(a.apiEmployeeIdentities))
	mux.HandleFunc("GET /api/v1/locations/{number}/employees/{employeeNumber}/full", a.apiAuth(a.apiEmployee))
	mux.HandleFunc("GET /api/v1/locations/{number}/employees/{employeeNumber}/identity", a.apiAuth(a.apiEmployeeIdentity))
	mux.HandleFunc("GET /api/v1/locations/{number}/employees", a.apiAuth(a.apiEmployees))
	mux.HandleFunc("GET /api/v1/locations/{number}/employees/{employeeNumber}", a.apiAuth(a.apiEmployee))
	return securityHeaders(mux)
}

func (a *App) render(w http.ResponseWriter, title, body string, data map[string]any) {
	data["Title"] = title
	tmpl := template.Must(template.New("layout").Funcs(templateFuncs()).Parse(layoutHTML + body))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("render: %v", err)
	}
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"selectedID": func(current *int64, id int64) bool {
			return current != nil && *current == id
		},
		"formatCents": func(cents *int64) string {
			if cents == nil {
				return ""
			}
			return formatDollars(*cents)
		},
		"formatWageInput": func(cents *int64) string {
			if cents == nil {
				return ""
			}
			return fmt.Sprintf("%.2f", float64(*cents)/100)
		},
		"formatMoney": func(cents int64) string {
			return formatDollars(cents)
		},
		"formatProductivity": formatProductivityGoal,
		"formatChartJSON":    formatChartJSON,
		"formatHours":        formatHours,
		"formatISODate":      formatISODate,
		"calendarDayPath":    calendarDayPath,
		"salesRowsForLabels": salesRowsForLabels,
		"salesDayparts": func() []string {
			return salesDayparts
		},
		"salesDestinations": func() []string {
			return salesDestinations
		},
		"salesRowsTotal": func(rows []SalesBreakdownRow) int64 {
			var total int64
			for _, row := range rows {
				total += row.Cents
			}
			return total
		},
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(value)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": message})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func css(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	fmt.Fprint(w, appCSS)
}

func absoluteBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func urlText(s string) string {
	return strings.ReplaceAll(s, " ", "+")
}

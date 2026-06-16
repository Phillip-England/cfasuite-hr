package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ledongthuc/pdf"
	"github.com/xuri/excelize/v2"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		cmdServe(os.Args[2:])
	case "init":
		cmdInit(os.Args[2:])
	case "db":
		cmdDB(os.Args[2:])
	case "set-admin":
		cmdSetAdmin(os.Args[2:])
	case "admin-env":
		cmdAdminEnv(os.Args[2:])
	case "token":
		cmdToken(os.Args[2:])
	case "api-context":
		cmdAPIContext(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Printf(`%s

Usage:
  cfasuite-hr serve [-addr :8217] [-db data/cfasuite-hr.db]
  cfasuite-hr init [-db data/cfasuite-hr.db]
  cfasuite-hr db path [-db data/cfasuite-hr.db]
  cfasuite-hr db reset -yes [-db data/cfasuite-hr.db]
  cfasuite-hr set-admin -username admin -password secret [-db data/cfasuite-hr.db]
  cfasuite-hr admin-env -username admin -password secret
  cfasuite-hr token create -name "Reporting" [-db data/cfasuite-hr.db]
  cfasuite-hr token list [-db data/cfasuite-hr.db]
  cfasuite-hr token delete -id 1 [-db data/cfasuite-hr.db]
  cfasuite-hr api-context -base-url https://hr.example.com

Environment:
  CFASUITE_DB_PATH
  CFASUITE_ADDR
  CFASUITE_ADMIN_USERNAME
  CFASUITE_ADMIN_PASSWORD
  CFASUITE_SESSION_SECRET
`, appName)
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", env("CFASUITE_ADDR", ":"+defaultPort), "HTTP listen address")
	dbPath := fs.String("db", env("CFASUITE_DB_PATH", defaultDBPath), "SQLite database path")
	fs.Parse(args)
	db, err := openDB(*dbPath)
	must(err)
	defer db.Close()
	must(migrate(db))
	app, err := newApp(db)
	must(err)
	log.Printf("%s listening on %s", appName, *addr)
	log.Printf("database: %s", abs(*dbPath))
	must(http.ListenAndServe(*addr, app.routes()))
}

func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	dbPath := fs.String("db", env("CFASUITE_DB_PATH", defaultDBPath), "SQLite database path")
	fs.Parse(args)
	db, err := openDB(*dbPath)
	must(err)
	defer db.Close()
	must(migrate(db))
	fmt.Println(abs(*dbPath))
}

func cmdDB(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: cfasuite-hr db path|reset")
		os.Exit(2)
	}
	switch args[0] {
	case "path":
		fs := flag.NewFlagSet("db path", flag.ExitOnError)
		dbPath := fs.String("db", env("CFASUITE_DB_PATH", defaultDBPath), "SQLite database path")
		fs.Parse(args[1:])
		fmt.Println(abs(*dbPath))
	case "reset":
		fs := flag.NewFlagSet("db reset", flag.ExitOnError)
		dbPath := fs.String("db", env("CFASUITE_DB_PATH", defaultDBPath), "SQLite database path")
		yes := fs.Bool("yes", false, "confirm database deletion")
		fs.Parse(args[1:])
		if !*yes {
			must(errors.New("db reset deletes all application data; rerun with -yes to confirm"))
		}
		path := abs(*dbPath)
		for _, removePath := range []string{path, path + "-wal", path + "-shm"} {
			if err := os.Remove(removePath); err != nil && !errors.Is(err, os.ErrNotExist) {
				must(err)
			}
		}
		db, err := openDB(path)
		must(err)
		defer db.Close()
		must(migrate(db))
		fmt.Printf("reset database: %s\n", path)
	default:
		fmt.Fprintf(os.Stderr, "unknown db command: %s\n", args[0])
		os.Exit(2)
	}
}

func cmdSetAdmin(args []string) {
	fs := flag.NewFlagSet("set-admin", flag.ExitOnError)
	dbPath := fs.String("db", env("CFASUITE_DB_PATH", defaultDBPath), "SQLite database path")
	username := fs.String("username", "", "admin username")
	password := fs.String("password", "", "admin password")
	fs.Parse(args)
	if *username == "" || *password == "" {
		must(errors.New("username and password are required"))
	}
	db, err := openDB(*dbPath)
	must(err)
	defer db.Close()
	must(migrate(db))
	hash, err := bcrypt.GenerateFromPassword([]byte(*password), bcrypt.DefaultCost)
	must(err)
	must(setSetting(db, "admin_username", *username))
	must(setSetting(db, "admin_password_hash", string(hash)))
	fmt.Printf("admin credentials saved in %s\n", abs(*dbPath))
}

func cmdAdminEnv(args []string) {
	fs := flag.NewFlagSet("admin-env", flag.ExitOnError)
	username := fs.String("username", "", "admin username")
	password := fs.String("password", "", "admin password")
	fs.Parse(args)
	if *username == "" || *password == "" {
		must(errors.New("username and password are required"))
	}
	fmt.Printf("export CFASUITE_ADMIN_USERNAME=%q\n", *username)
	fmt.Printf("export CFASUITE_ADMIN_PASSWORD=%q\n", *password)
}

func cmdToken(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: cfasuite-hr token create|list|delete")
		os.Exit(2)
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("token create", flag.ExitOnError)
		dbPath := fs.String("db", env("CFASUITE_DB_PATH", defaultDBPath), "SQLite database path")
		name := fs.String("name", "", "token name")
		fs.Parse(args[1:])
		if *name == "" {
			must(errors.New("token name is required"))
		}
		db := mustDB(*dbPath)
		defer db.Close()
		raw, token, err := createToken(db, *name)
		must(err)
		fmt.Printf("name: %s\nid: %d\nprefix: %s\ntoken: %s\n", token.Name, token.ID, token.Prefix, raw)
	case "list":
		fs := flag.NewFlagSet("token list", flag.ExitOnError)
		dbPath := fs.String("db", env("CFASUITE_DB_PATH", defaultDBPath), "SQLite database path")
		fs.Parse(args[1:])
		db := mustDB(*dbPath)
		defer db.Close()
		tokens, err := listTokens(db)
		must(err)
		for _, token := range tokens {
			last := ""
			if token.LastUsedAt != nil {
				last = *token.LastUsedAt
			}
			fmt.Printf("%d\t%s\t%s\t%s\t%s\n", token.ID, token.Name, token.Prefix, token.CreatedAt.Format(time.RFC3339), last)
		}
	case "delete":
		fs := flag.NewFlagSet("token delete", flag.ExitOnError)
		dbPath := fs.String("db", env("CFASUITE_DB_PATH", defaultDBPath), "SQLite database path")
		id := fs.Int64("id", 0, "token id")
		fs.Parse(args[1:])
		if *id == 0 {
			must(errors.New("token id is required"))
		}
		db := mustDB(*dbPath)
		defer db.Close()
		_, err := db.Exec(`DELETE FROM api_tokens WHERE id = ?`, *id)
		must(err)
		fmt.Println("deleted")
	default:
		fmt.Fprintf(os.Stderr, "unknown token command: %s\n", args[0])
		os.Exit(2)
	}
}

func cmdAPIContext(args []string) {
	fs := flag.NewFlagSet("api-context", flag.ExitOnError)
	baseURL := ""
	fs.StringVar(&baseURL, "base-url", "", "public base URL where cfasuite-hr is running")
	fs.StringVar(&baseURL, "endpoint", "", "public base URL where cfasuite-hr is running")
	fs.Parse(args)
	if baseURL == "" {
		must(errors.New("api-context requires -base-url, for example: cfasuite-hr api-context -base-url https://hr.example.com"))
	}
	fmt.Print(apiContext(baseURL))
}

func mustDB(path string) *sql.DB {
	db, err := openDB(path)
	must(err)
	must(migrate(db))
	return db
}

func openDB(path string) (*sql.DB, error) {
	dir := filepath.Dir(path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, db.Ping()
}

func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS locations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			number TEXT NOT NULL UNIQUE,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS roles (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			location_id INTEGER NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(location_id, name)
		)`,
		`CREATE TABLE IF NOT EXISTS departments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			location_id INTEGER NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(location_id, name)
		)`,
		`CREATE TABLE IF NOT EXISTS employees (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			location_id INTEGER NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
			employee_name TEXT NOT NULL,
			employee_number TEXT NOT NULL,
			job TEXT NOT NULL,
			role_id INTEGER REFERENCES roles(id) ON DELETE SET NULL,
			department_id INTEGER REFERENCES departments(id) ON DELETE SET NULL,
			wage_rate_cents INTEGER,
			wage_pay_type TEXT NOT NULL DEFAULT '',
			exclude_from_labor INTEGER NOT NULL DEFAULT 0,
			employee_status TEXT NOT NULL,
			location_latest_start_date TEXT NOT NULL,
			birth_date TEXT,
			clock_in_pin TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(location_id, employee_number)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_employees_location ON employees(location_id)`,
		`CREATE TABLE IF NOT EXISTS api_tokens (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			prefix TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_used_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS login_attempts (
			ip TEXT PRIMARY KEY,
			attempts INTEGER NOT NULL,
			banned INTEGER NOT NULL DEFAULT 0,
			last_attempt_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS daily_sales (
			location_id INTEGER NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
			business_date TEXT NOT NULL,
			total_cents INTEGER NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY(location_id, business_date)
		)`,
		`CREATE TABLE IF NOT EXISTS daily_sales_breakdowns (
			location_id INTEGER NOT NULL,
			business_date TEXT NOT NULL,
			group_type TEXT NOT NULL,
			label TEXT NOT NULL,
			cents INTEGER NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY(location_id, business_date, group_type, label),
			FOREIGN KEY(location_id, business_date) REFERENCES daily_sales(location_id, business_date) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_daily_sales_location_date ON daily_sales(location_id, business_date)`,
		`CREATE TABLE IF NOT EXISTS daily_labor (
			location_id INTEGER NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
			business_date TEXT NOT NULL,
			total_minutes INTEGER NOT NULL,
			overtime_minutes INTEGER NOT NULL DEFAULT 0,
			total_wages_cents INTEGER NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY(location_id, business_date)
		)`,
		`CREATE TABLE IF NOT EXISTS daily_labor_breakdowns (
			location_id INTEGER NOT NULL,
			business_date TEXT NOT NULL,
			group_type TEXT NOT NULL,
			label TEXT NOT NULL,
			minutes INTEGER NOT NULL,
			overtime_minutes INTEGER NOT NULL DEFAULT 0,
			wages_cents INTEGER NOT NULL,
			overtime_wages_cents INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY(location_id, business_date, group_type, label),
			FOREIGN KEY(location_id, business_date) REFERENCES daily_labor(location_id, business_date) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_daily_labor_location_date ON daily_labor(location_id, business_date)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	if err := ensureColumn(db, "employees", "birth_date", "TEXT"); err != nil {
		return err
	}
	if err := ensureColumn(db, "employees", "role_id", "INTEGER REFERENCES roles(id) ON DELETE SET NULL"); err != nil {
		return err
	}
	if err := ensureColumn(db, "employees", "department_id", "INTEGER REFERENCES departments(id) ON DELETE SET NULL"); err != nil {
		return err
	}
	if err := ensureColumn(db, "employees", "wage_rate_cents", "INTEGER"); err != nil {
		return err
	}
	if err := ensureColumn(db, "employees", "wage_pay_type", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(db, "employees", "exclude_from_labor", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := ensureColumn(db, "employees", "clock_in_pin", "TEXT"); err != nil {
		return err
	}
	if err := ensureColumn(db, "daily_labor_breakdowns", "overtime_wages_cents", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	return nil
}

func ensureColumn(db *sql.DB, table, column, definition string) error {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + definition)
	return err
}

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
	return &App{db: db, sessionSecret: []byte(secret), adminUsername: username, adminPassword: password, adminHash: adminHash}, nil
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
	mux.HandleFunc("GET /locations/{id}/calendar", a.requireAdmin(a.locationCalendar))
	mux.HandleFunc("GET /locations/{id}/calendar/{date}", a.requireAdmin(a.locationCalendarDay))
	mux.HandleFunc("POST /locations/{id}/calendar/{date}/sales", a.requireAdmin(a.locationSalesUpload))
	mux.HandleFunc("POST /locations/{id}/calendar/{date}/labor", a.requireAdmin(a.locationLaborUpload))
	mux.HandleFunc("GET /locations/{id}/sales", a.requireAdmin(a.locationSales))
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
	mux.HandleFunc("GET /admin/api/context", a.requireAdmin(a.contextPage))
	mux.HandleFunc("GET /assets/app.css", css)
	mux.HandleFunc("GET /api/v1/locations", a.apiAuth(a.apiLocations))
	mux.HandleFunc("GET /api/v1/locations/{number}/employees", a.apiAuth(a.apiEmployees))
	mux.HandleFunc("GET /api/v1/locations/{number}/employees/{employeeNumber}", a.apiAuth(a.apiEmployee))
	return securityHeaders(mux)
}

func (a *App) loginPage(w http.ResponseWriter, r *http.Request) {
	a.render(w, "Login", loginHTML, map[string]any{"Configured": a.adminConfigured(), "Error": r.URL.Query().Get("error"), "LoggedOut": true})
}

func (a *App) loginPost(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	_ = purgeOldAttempts(a.db)
	banned, _ := isBanned(a.db, ip)
	if banned {
		http.Error(w, "too many invalid attempts; try again later", http.StatusTooManyRequests)
		return
	}
	if !a.adminConfigured() {
		http.Redirect(w, r, "/login?error="+urlText("admin credentials are not configured"), http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if a.validAdmin(r.FormValue("username"), r.FormValue("password")) {
		_ = clearAttempt(a.db, ip)
		a.setSession(w, r.FormValue("username"))
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	_ = recordFailedAttempt(a.db, ip)
	http.Redirect(w, r, "/login?error="+urlText("invalid username or password"), http.StatusSeeOther)
}

func (a *App) logoutPost(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	locations, err := listLocations(a.db)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, "Locations", dashboardHTML, map[string]any{"Locations": locations, "Import": r.URL.Query()})
}

func (a *App) locationNew(w http.ResponseWriter, r *http.Request) {
	a.render(w, "New Location", locationFormHTML, map[string]any{"Location": Location{}, "Action": "/locations", "Mode": "Create"})
}

func (a *App) locationCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, err := createLocation(a.db, r.FormValue("name"), r.FormValue("number"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/locations/%d", id), http.StatusSeeOther)
}

func (a *App) locationShow(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	loc, err := getLocation(a.db, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	employees, err := listEmployees(a.db, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	roles, err := listRoles(a.db, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	departments, err := listDepartments(a.db, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, loc.Name, locationShowHTML, map[string]any{
		"Location":         loc,
		"Employees":        employees,
		"Roles":            roles,
		"Departments":      departments,
		"JobOptions":       employeeJobOptions(employees),
		"AssignmentStatus": employeeAssignmentStatus(employees),
		"Import":           r.URL.Query(),
	})
}

func (a *App) locationDetails(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	loc, err := getLocation(a.db, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	employees, err := listEmployees(a.db, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, loc.Name+" Employee Details", locationDetailsHTML, map[string]any{
		"Location":  loc,
		"Employees": employees,
		"Import":    r.URL.Query(),
	})
}

func (a *App) locationPay(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	loc, err := getLocation(a.db, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	employees, err := listEmployees(a.db, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, loc.Name+" Employee Pay", locationPayHTML, map[string]any{
		"Location":  loc,
		"Employees": employees,
		"Import":    r.URL.Query(),
	})
}

func (a *App) locationCalendar(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	loc, err := getLocation(a.db, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	month, err := calendarMonthFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	salesDates, err := salesDatesForCalendar(a.db, id, month)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	laborDates, err := laborDatesForCalendar(a.db, id, month)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, loc.Name+" Calendar", locationCalendarHTML, map[string]any{
		"Location":   loc,
		"MonthLabel": month.Format("January 2006"),
		"MonthValue": month.Format("2006-01"),
		"PrevMonth":  month.AddDate(0, -1, 0).Format("2006-01"),
		"NextMonth":  month.AddDate(0, 1, 0).Format("2006-01"),
		"Days":       calendarDays(month, time.Now(), salesDates, laborDates),
	})
}

func (a *App) locationCalendarDay(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	loc, err := getLocation(a.db, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	date, err := time.ParseInLocation("2006-01-02", r.PathValue("date"), time.Local)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !date.Before(startOfDay(time.Now())) {
		http.Error(w, "calendar days can only be opened after the day is complete", http.StatusNotFound)
		return
	}
	sales, err := getDailySales(a.db, loc.ID, date.Format("2006-01-02"))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	labor, err := getDailyLabor(a.db, loc.ID, date.Format("2006-01-02"))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	nextDay := date.AddDate(0, 0, 1)
	nextDayURL := ""
	if nextDay.Before(startOfDay(time.Now())) {
		nextDayURL = fmt.Sprintf("/locations/%d/calendar/%s", loc.ID, nextDay.Format("2006-01-02"))
	}
	a.render(w, loc.Name+" "+date.Format("January 2, 2006"), locationCalendarDayHTML, map[string]any{
		"Location":    loc,
		"Date":        date.Format("2006-01-02"),
		"DateLabel":   date.Format("Monday, January 2, 2006"),
		"MonthValue":  time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, date.Location()).Format("2006-01"),
		"BackToMonth": fmt.Sprintf("/locations/%d/calendar?month=%s", loc.ID, date.Format("2006-01")),
		"PrevDayURL":  fmt.Sprintf("/locations/%d/calendar/%s", loc.ID, date.AddDate(0, 0, -1).Format("2006-01-02")),
		"NextDayURL":  nextDayURL,
		"Sales":       sales,
		"Labor":       labor,
		"Import":      r.URL.Query(),
	})
}

func (a *App) locationSalesUpload(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := getLocation(a.db, id); err != nil {
		http.NotFound(w, r)
		return
	}
	date, err := time.ParseInLocation("2006-01-02", r.PathValue("date"), time.Local)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !date.Before(startOfDay(time.Now())) {
		http.Error(w, "sales can only be uploaded for completed past days", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("daypart_activity")
	if err != nil {
		http.Error(w, "daypart activity PDF file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	report, err := parseDaypartActivityPDF(file, header)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	selectedDate := date.Format("2006-01-02")
	if report.BusinessDate != selectedDate {
		http.Error(w, fmt.Sprintf("report business date %s does not match selected date %s", report.BusinessDate, selectedDate), http.StatusBadRequest)
		return
	}
	if err := saveDailySales(a.db, id, report); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/locations/%d/calendar/%s?sales_imported=1", id, selectedDate), http.StatusSeeOther)
}

func (a *App) locationLaborUpload(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := getLocation(a.db, id); err != nil {
		http.NotFound(w, r)
		return
	}
	date, err := time.ParseInLocation("2006-01-02", r.PathValue("date"), time.Local)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !date.Before(startOfDay(time.Now())) {
		http.Error(w, "labor can only be uploaded for completed past days", http.StatusBadRequest)
		return
	}
	report, err := timePunchReportFromRequest(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	employees, err := listEmployees(a.db, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	applyEmployeeAssignments(&report, employees)
	finalizeLaborReport(&report, employees)
	selectedDate := date.Format("2006-01-02")
	if !laborReportIncludesDate(report, selectedDate) {
		http.Error(w, fmt.Sprintf("time punch report does not include selected date %s", selectedDate), http.StatusBadRequest)
		return
	}
	if err := saveDailyLabor(a.db, id, selectedDate, report); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/locations/%d/calendar/%s?labor_imported=1", id, selectedDate), http.StatusSeeOther)
}

func (a *App) locationSales(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	loc, err := getLocation(a.db, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	start, end, err := salesRangeFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sales, err := listDailySales(a.db, id, start.Format("2006-01-02"), end.Format("2006-01-02"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	missing := missingSalesDates(start, end, sales)
	a.render(w, loc.Name+" Sales", locationSalesHTML, map[string]any{
		"Location":          loc,
		"StartDate":         start.Format("2006-01-02"),
		"EndDate":           end.Format("2006-01-02"),
		"MissingDates":      missing,
		"Complete":          len(missing) == 0,
		"DailyRows":         salesDailyRows(sales),
		"DaypartRows":       aggregateSalesRows(sales, "daypart"),
		"DestinationRows":   aggregateSalesRows(sales, "destination"),
		"DayOfWeekRows":     dayOfWeekSalesRows(sales),
		"SelectedDateCount": requiredSalesDateCount(start, end),
	})
}

func (a *App) locationDocuments(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	loc, err := getLocation(a.db, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a.render(w, loc.Name+" Documents", locationDocumentsHTML, map[string]any{"Location": loc, "Import": r.URL.Query()})
}

func (a *App) documentTimePunchWageUpload(w http.ResponseWriter, r *http.Request) {
	locationID, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := getLocation(a.db, locationID); err != nil {
		http.NotFound(w, r)
		return
	}
	report, err := timePunchReportFromRequest(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	employees, err := listEmployees(a.db, locationID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := updateEmployeeWagesFromReport(a.db, report, employees); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/locations/%d/documents?wages_imported=1", locationID), http.StatusSeeOther)
}

func (a *App) locationEdit(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	loc, err := getLocation(a.db, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a.render(w, loc.Name+" Edit", locationEditHTML, map[string]any{"Location": loc, "Saved": r.URL.Query().Get("saved")})
}

func (a *App) locationUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := updateLocation(a.db, id, r.FormValue("name"), r.FormValue("number")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/locations/%d/edit?saved=1", id), http.StatusSeeOther)
}

func (a *App) locationDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := a.db.Exec(`DELETE FROM locations WHERE id = ?`, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) locationUpload(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("bio")
	if err != nil {
		http.Error(w, "bio .xlsx file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	result, err := importBio(a.db, id, file, header)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/locations/%d/documents?added=%d&updated=%d&removed=%d&skipped=%d", id, result.Added, result.Updated, result.Removed, result.Skipped), http.StatusSeeOther)
}

func (a *App) birthdayUpload(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := getLocation(a.db, id); err != nil {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("birthdays")
	if err != nil {
		http.Error(w, "birthday report .xlsx file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	result, err := importBirthdays(a.db, id, file, header)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/locations/%d/documents?birthday_updated=%d&birthday_skipped=%d", id, result.Updated, result.Skipped), http.StatusSeeOther)
}

func (a *App) pinUpload(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := getLocation(a.db, id); err != nil {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("pins")
	if err != nil {
		http.Error(w, "PIN report PDF file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	result, err := importPins(a.db, id, file, header)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/locations/%d/documents?pin_updated=%d&pin_skipped=%d", id, result.Updated, result.Skipped), http.StatusSeeOther)
}

func (a *App) locationRolesUpdate(w http.ResponseWriter, r *http.Request) {
	a.updateEmployeeRoleAssignments(w, r)
}

func (a *App) locationAssignmentsUpdate(w http.ResponseWriter, r *http.Request) {
	switch r.FormValue("assignment") {
	case "department":
		a.updateEmployeeDepartmentAssignments(w, r)
	case "wage":
		a.updateEmployeeWages(w, r)
	case "labor_exclusion":
		a.updateEmployeeLaborExclusion(w, r)
	default:
		a.updateEmployeeRoleAssignments(w, r)
	}
}

func (a *App) updateEmployeeWages(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := getLocation(a.db, id); err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	employeeIDs, err := parseInt64Values(r.Form["employee_id"])
	if err != nil {
		http.Error(w, "invalid employee selection", http.StatusBadRequest)
		return
	}
	payType := strings.TrimSpace(strings.ToLower(r.FormValue("wage_pay_type")))
	wage := strings.TrimSpace(r.FormValue("wage_rate"))
	if payType != "" && payType != "hourly" && payType != "salary" {
		http.Error(w, "pay type must be hourly or salary", http.StatusBadRequest)
		return
	}
	var cents *int64
	if wage != "" {
		if payType != "hourly" && payType != "salary" {
			http.Error(w, "pay type is required when setting a wage amount", http.StatusBadRequest)
			return
		}
		parsed, err := parseWageCents(wage)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		cents = &parsed
	}
	updated, err := assignEmployeeWage(a.db, id, employeeIDs, cents, payType)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if wantsAsync(r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/locations/%d/pay?wages_assigned=%d", id, updated), http.StatusSeeOther)
}

func (a *App) updateEmployeeLaborExclusion(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := getLocation(a.db, id); err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	employeeIDs, err := parseInt64Values(r.Form["employee_id"])
	if err != nil {
		http.Error(w, "invalid employee selection", http.StatusBadRequest)
		return
	}
	excluded := false
	for _, value := range r.Form["exclude_from_labor"] {
		if value == "1" {
			excluded = true
			break
		}
	}
	updated, err := assignEmployeeLaborExclusion(a.db, id, employeeIDs, excluded)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if wantsAsync(r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/locations/%d/pay?labor_exclusions=%d", id, updated), http.StatusSeeOther)
}

func (a *App) updateEmployeeRoleAssignments(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := getLocation(a.db, id); err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	employeeIDs, err := parseInt64Values(r.Form["employee_id"])
	if err != nil {
		http.Error(w, "invalid employee selection", http.StatusBadRequest)
		return
	}
	if len(employeeIDs) == 0 {
		if wantsAsync(r) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/locations/%d?roles_assigned=0", id), http.StatusSeeOther)
		return
	}
	var roleID *int64
	if raw := strings.TrimSpace(r.FormValue("role_id")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			http.Error(w, "invalid role", http.StatusBadRequest)
			return
		}
		if _, err := getRole(a.db, id, parsed); err != nil {
			http.Error(w, "role not found", http.StatusBadRequest)
			return
		}
		roleID = &parsed
	}
	updated, err := assignEmployeeRole(a.db, id, employeeIDs, roleID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if wantsAsync(r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/locations/%d?roles_assigned=%d", id, updated), http.StatusSeeOther)
}

func (a *App) updateEmployeeDepartmentAssignments(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := getLocation(a.db, id); err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	employeeIDs, err := parseInt64Values(r.Form["employee_id"])
	if err != nil {
		http.Error(w, "invalid employee selection", http.StatusBadRequest)
		return
	}
	if len(employeeIDs) == 0 {
		if wantsAsync(r) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/locations/%d?departments_assigned=0", id), http.StatusSeeOther)
		return
	}
	var departmentID *int64
	if raw := strings.TrimSpace(r.FormValue("department_id")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			http.Error(w, "invalid department", http.StatusBadRequest)
			return
		}
		if _, err := getDepartment(a.db, id, parsed); err != nil {
			http.Error(w, "department not found", http.StatusBadRequest)
			return
		}
		departmentID = &parsed
	}
	updated, err := assignEmployeeDepartment(a.db, id, employeeIDs, departmentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if wantsAsync(r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/locations/%d?departments_assigned=%d", id, updated), http.StatusSeeOther)
}

func wantsAsync(r *http.Request) bool {
	return r.Header.Get("X-Requested-With") == "fetch"
}

func (a *App) laborPage(w http.ResponseWriter, r *http.Request) {
	locationID, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	loc, err := getLocation(a.db, locationID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	start, end, err := laborRangeFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	labor, err := listDailyLabor(a.db, locationID, start.Format("2006-01-02"), end.Format("2006-01-02"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	missing := missingLaborDates(start, end, labor)
	a.render(w, loc.Name+" Labor", laborHTML, laborPageData(loc, start, end, labor, missing, nil))
}

func (a *App) laborUpload(w http.ResponseWriter, r *http.Request) {
	locationID, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	loc, err := getLocation(a.db, locationID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	report, err := timePunchReportFromRequest(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	employees, err := listEmployees(a.db, locationID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	applyEmployeeAssignments(&report, employees)
	finalizeLaborReport(&report, employees)
	start, end, _ := laborRangeFromRequest(r)
	labor, _ := listDailyLabor(a.db, locationID, start.Format("2006-01-02"), end.Format("2006-01-02"))
	missing := missingLaborDates(start, end, labor)
	a.render(w, loc.Name+" Labor", laborHTML, laborPageData(loc, start, end, labor, missing, &report))
}

func (a *App) rolesPage(w http.ResponseWriter, r *http.Request) {
	locationID, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	loc, err := getLocation(a.db, locationID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	roles, err := listRoles(a.db, locationID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, loc.Name+" Roles", rolesHTML, map[string]any{"Roles": roles, "SelectedLocation": loc})
}

func (a *App) roleCreate(w http.ResponseWriter, r *http.Request) {
	locationID, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := getLocation(a.db, locationID); err != nil {
		http.Error(w, "location not found", http.StatusBadRequest)
		return
	}
	if _, err := createRole(a.db, locationID, r.FormValue("name")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/locations/%d/roles", locationID), http.StatusSeeOther)
}

func (a *App) roleUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	role, err := getRoleByID(a.db, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := updateRole(a.db, id, r.FormValue("name")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/locations/%d/roles", role.LocationID), http.StatusSeeOther)
}

func (a *App) roleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	role, err := getRoleByID(a.db, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := a.db.Exec(`DELETE FROM roles WHERE id = ?`, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/locations/%d/roles", role.LocationID), http.StatusSeeOther)
}

func (a *App) departmentsPage(w http.ResponseWriter, r *http.Request) {
	locationID, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	loc, err := getLocation(a.db, locationID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	departments, err := listDepartments(a.db, locationID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, loc.Name+" Departments", departmentsHTML, map[string]any{"Departments": departments, "SelectedLocation": loc})
}

func (a *App) departmentCreate(w http.ResponseWriter, r *http.Request) {
	locationID, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := getLocation(a.db, locationID); err != nil {
		http.Error(w, "location not found", http.StatusBadRequest)
		return
	}
	if _, err := createDepartment(a.db, locationID, r.FormValue("name")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/locations/%d/departments", locationID), http.StatusSeeOther)
}

func (a *App) departmentUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	department, err := getDepartmentByID(a.db, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := updateDepartment(a.db, id, r.FormValue("name")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/locations/%d/departments", department.LocationID), http.StatusSeeOther)
}

func (a *App) departmentDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	department, err := getDepartmentByID(a.db, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := a.db.Exec(`DELETE FROM departments WHERE id = ?`, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/locations/%d/departments", department.LocationID), http.StatusSeeOther)
}

func (a *App) tokensPage(w http.ResponseWriter, r *http.Request) {
	tokens, err := listTokens(a.db)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, "API Tokens", tokensHTML, map[string]any{"Tokens": tokens})
}

func (a *App) tokenCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	raw, token, err := createToken(a.db, r.FormValue("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	tokens, _ := listTokens(a.db)
	a.render(w, "API Tokens", tokensHTML, map[string]any{"Tokens": tokens, "NewToken": raw, "NewTokenName": token.Name})
}

func (a *App) tokenDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := a.db.Exec(`DELETE FROM api_tokens WHERE id = ?`, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/tokens", http.StatusSeeOther)
}

func (a *App) docsPage(w http.ResponseWriter, r *http.Request) {
	a.render(w, "API Docs", docsHTML, map[string]any{"BaseURL": absoluteBaseURL(r), "Context": apiContext(absoluteBaseURL(r))})
}

func (a *App) contextPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, apiContext(absoluteBaseURL(r)))
}

func (a *App) apiLocations(w http.ResponseWriter, r *http.Request) {
	locations, err := listLocations(a.db)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"locations": locations})
}

func (a *App) apiEmployees(w http.ResponseWriter, r *http.Request) {
	loc, err := getLocationByNumber(a.db, r.PathValue("number"))
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "location not found")
		return
	}
	employees, err := listEmployees(a.db, loc.ID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"location": loc, "employees": employees})
}

func (a *App) apiEmployee(w http.ResponseWriter, r *http.Request) {
	loc, err := getLocationByNumber(a.db, r.PathValue("number"))
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "location not found")
		return
	}
	employee, err := getEmployee(a.db, loc.ID, r.PathValue("employeeNumber"))
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "employee not found")
		return
	}
	writeJSON(w, map[string]any{"location": loc, "employee": employee})
}

func (a *App) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.validSession(r) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (a *App) apiAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" {
			token = r.Header.Get("X-API-Token")
		}
		if token == "" || !validToken(a.db, token) {
			writeJSONError(w, http.StatusUnauthorized, "valid API token required")
			return
		}
		next(w, r)
	}
}

func (a *App) adminConfigured() bool {
	return a.adminUsername != "" && (a.adminPassword != "" || a.adminHash != "")
}

func (a *App) validAdmin(username, password string) bool {
	if username != a.adminUsername {
		return false
	}
	if a.adminPassword != "" {
		return subtleEqual(a.adminPassword, password)
	}
	return bcrypt.CompareHashAndPassword([]byte(a.adminHash), []byte(password)) == nil
}

func (a *App) setSession(w http.ResponseWriter, username string) {
	expires := time.Now().Add(sessionTTL).Unix()
	payload := fmt.Sprintf("%s|%d", username, expires)
	sig := sign(a.sessionSecret, payload)
	value := base64.RawURLEncoding.EncodeToString([]byte(payload + "|" + sig))
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: value, Path: "/", Expires: time.Unix(expires, 0), HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func (a *App) validSession(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return false
	}
	parts := strings.Split(string(decoded), "|")
	if len(parts) != 3 {
		return false
	}
	payload := parts[0] + "|" + parts[1]
	if !subtleEqual(sign(a.sessionSecret, payload), parts[2]) {
		return false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	return parts[0] == a.adminUsername
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

func listLocations(db *sql.DB) ([]Location, error) {
	rows, err := db.Query(`SELECT l.id, l.name, l.number, l.created_at, l.updated_at, COUNT(e.id)
		FROM locations l
		LEFT JOIN employees e ON e.location_id = l.id
		GROUP BY l.id
		ORDER BY l.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var locations []Location
	for rows.Next() {
		var loc Location
		var created, updated string
		if err := rows.Scan(&loc.ID, &loc.Name, &loc.Number, &created, &updated, &loc.Employees); err != nil {
			return nil, err
		}
		loc.CreatedAt = parseTime(created)
		loc.UpdatedAt = parseTime(updated)
		locations = append(locations, loc)
	}
	return locations, rows.Err()
}

func createLocation(db *sql.DB, name, number string) (int64, error) {
	name = strings.TrimSpace(name)
	number = strings.TrimSpace(number)
	if name == "" || number == "" {
		return 0, errors.New("name and number are required")
	}
	res, err := db.Exec(`INSERT INTO locations (name, number) VALUES (?, ?)`, name, number)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func updateLocation(db *sql.DB, id int64, name, number string) error {
	name = strings.TrimSpace(name)
	number = strings.TrimSpace(number)
	if name == "" || number == "" {
		return errors.New("name and number are required")
	}
	_, err := db.Exec(`UPDATE locations SET name = ?, number = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, name, number, id)
	return err
}

func getLocation(db *sql.DB, id int64) (Location, error) {
	var loc Location
	var created, updated string
	err := db.QueryRow(`SELECT id, name, number, created_at, updated_at FROM locations WHERE id = ?`, id).Scan(&loc.ID, &loc.Name, &loc.Number, &created, &updated)
	loc.CreatedAt = parseTime(created)
	loc.UpdatedAt = parseTime(updated)
	return loc, err
}

func getLocationByNumber(db *sql.DB, number string) (Location, error) {
	var loc Location
	var created, updated string
	err := db.QueryRow(`SELECT id, name, number, created_at, updated_at FROM locations WHERE number = ?`, number).Scan(&loc.ID, &loc.Name, &loc.Number, &created, &updated)
	loc.CreatedAt = parseTime(created)
	loc.UpdatedAt = parseTime(updated)
	return loc, err
}

func listRoles(db *sql.DB, locationID int64) ([]Role, error) {
	rows, err := db.Query(`SELECT r.id, r.location_id, r.name, r.created_at, r.updated_at, COUNT(e.id)
		FROM roles r
		LEFT JOIN employees e ON e.role_id = r.id
		WHERE r.location_id = ?
		GROUP BY r.id
		ORDER BY r.name`, locationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roles []Role
	for rows.Next() {
		var role Role
		var created, updated string
		if err := rows.Scan(&role.ID, &role.LocationID, &role.Name, &created, &updated, &role.Employees); err != nil {
			return nil, err
		}
		role.CreatedAt = parseTime(created)
		role.UpdatedAt = parseTime(updated)
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

func createRole(db *sql.DB, locationID int64, name string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, errors.New("role name is required")
	}
	res, err := db.Exec(`INSERT INTO roles (location_id, name) VALUES (?, ?)`, locationID, name)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func updateRole(db *sql.DB, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("role name is required")
	}
	_, err := db.Exec(`UPDATE roles SET name = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, name, id)
	return err
}

func getRole(db *sql.DB, locationID, id int64) (Role, error) {
	var role Role
	var created, updated string
	err := db.QueryRow(`SELECT id, location_id, name, created_at, updated_at FROM roles WHERE location_id = ? AND id = ?`, locationID, id).Scan(&role.ID, &role.LocationID, &role.Name, &created, &updated)
	role.CreatedAt = parseTime(created)
	role.UpdatedAt = parseTime(updated)
	return role, err
}

func getRoleByID(db *sql.DB, id int64) (Role, error) {
	var role Role
	var created, updated string
	err := db.QueryRow(`SELECT id, location_id, name, created_at, updated_at FROM roles WHERE id = ?`, id).Scan(&role.ID, &role.LocationID, &role.Name, &created, &updated)
	role.CreatedAt = parseTime(created)
	role.UpdatedAt = parseTime(updated)
	return role, err
}

func listDepartments(db *sql.DB, locationID int64) ([]Department, error) {
	rows, err := db.Query(`SELECT d.id, d.location_id, d.name, d.created_at, d.updated_at, COUNT(e.id)
		FROM departments d
		LEFT JOIN employees e ON e.department_id = d.id
		WHERE d.location_id = ?
		GROUP BY d.id
		ORDER BY d.name`, locationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var departments []Department
	for rows.Next() {
		var department Department
		var created, updated string
		if err := rows.Scan(&department.ID, &department.LocationID, &department.Name, &created, &updated, &department.Employees); err != nil {
			return nil, err
		}
		department.CreatedAt = parseTime(created)
		department.UpdatedAt = parseTime(updated)
		departments = append(departments, department)
	}
	return departments, rows.Err()
}

func createDepartment(db *sql.DB, locationID int64, name string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, errors.New("department name is required")
	}
	res, err := db.Exec(`INSERT INTO departments (location_id, name) VALUES (?, ?)`, locationID, name)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func updateDepartment(db *sql.DB, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("department name is required")
	}
	_, err := db.Exec(`UPDATE departments SET name = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, name, id)
	return err
}

func getDepartment(db *sql.DB, locationID, id int64) (Department, error) {
	var department Department
	var created, updated string
	err := db.QueryRow(`SELECT id, location_id, name, created_at, updated_at FROM departments WHERE location_id = ? AND id = ?`, locationID, id).Scan(&department.ID, &department.LocationID, &department.Name, &created, &updated)
	department.CreatedAt = parseTime(created)
	department.UpdatedAt = parseTime(updated)
	return department, err
}

func getDepartmentByID(db *sql.DB, id int64) (Department, error) {
	var department Department
	var created, updated string
	err := db.QueryRow(`SELECT id, location_id, name, created_at, updated_at FROM departments WHERE id = ?`, id).Scan(&department.ID, &department.LocationID, &department.Name, &created, &updated)
	department.CreatedAt = parseTime(created)
	department.UpdatedAt = parseTime(updated)
	return department, err
}

func assignEmployeeRole(db *sql.DB, locationID int64, employeeIDs []int64, roleID *int64) (int, error) {
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	updated := 0
	for _, employeeID := range employeeIDs {
		var res sql.Result
		if roleID == nil {
			res, err = tx.Exec(`UPDATE employees SET role_id = NULL, updated_at = CURRENT_TIMESTAMP WHERE location_id = ? AND id = ?`, locationID, employeeID)
		} else {
			res, err = tx.Exec(`UPDATE employees SET role_id = ?, updated_at = CURRENT_TIMESTAMP WHERE location_id = ? AND id = ?`, *roleID, locationID, employeeID)
		}
		if err != nil {
			return 0, err
		}
		affected, _ := res.RowsAffected()
		updated += int(affected)
	}
	return updated, tx.Commit()
}

func assignEmployeeDepartment(db *sql.DB, locationID int64, employeeIDs []int64, departmentID *int64) (int, error) {
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	updated := 0
	for _, employeeID := range employeeIDs {
		var res sql.Result
		if departmentID == nil {
			res, err = tx.Exec(`UPDATE employees SET department_id = NULL, updated_at = CURRENT_TIMESTAMP WHERE location_id = ? AND id = ?`, locationID, employeeID)
		} else {
			res, err = tx.Exec(`UPDATE employees SET department_id = ?, updated_at = CURRENT_TIMESTAMP WHERE location_id = ? AND id = ?`, *departmentID, locationID, employeeID)
		}
		if err != nil {
			return 0, err
		}
		affected, _ := res.RowsAffected()
		updated += int(affected)
	}
	return updated, tx.Commit()
}

func assignEmployeeLaborExclusion(db *sql.DB, locationID int64, employeeIDs []int64, excluded bool) (int, error) {
	value := 0
	if excluded {
		value = 1
	}
	updated := 0
	for _, employeeID := range employeeIDs {
		res, err := db.Exec(`UPDATE employees SET exclude_from_labor = ?, updated_at = CURRENT_TIMESTAMP WHERE location_id = ? AND id = ?`, value, locationID, employeeID)
		if err != nil {
			return 0, err
		}
		affected, _ := res.RowsAffected()
		updated += int(affected)
	}
	return updated, nil
}

func assignEmployeeWage(db *sql.DB, locationID int64, employeeIDs []int64, wageRateCents *int64, payType string) (int, error) {
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	updated := 0
	for _, employeeID := range employeeIDs {
		var employeeNumber string
		if err := tx.QueryRow(`SELECT employee_number FROM employees WHERE location_id = ? AND id = ?`, locationID, employeeID).Scan(&employeeNumber); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return 0, err
		}
		var res sql.Result
		if wageRateCents == nil {
			res, err = tx.Exec(`UPDATE employees SET wage_rate_cents = NULL, wage_pay_type = ?, updated_at = CURRENT_TIMESTAMP WHERE employee_number = ?`, payType, employeeNumber)
		} else {
			res, err = tx.Exec(`UPDATE employees SET wage_rate_cents = ?, wage_pay_type = ?, updated_at = CURRENT_TIMESTAMP WHERE employee_number = ?`, *wageRateCents, payType, employeeNumber)
		}
		if err != nil {
			return 0, err
		}
		affected, _ := res.RowsAffected()
		updated += int(affected)
	}
	return updated, tx.Commit()
}

func listEmployees(db *sql.DB, locationID int64) ([]Employee, error) {
	rows, err := db.Query(`SELECT e.id, e.location_id, e.employee_name, e.employee_number, e.job, e.role_id, r.name, e.department_id, d.name, e.wage_rate_cents, e.wage_pay_type, e.exclude_from_labor, e.employee_status, e.location_latest_start_date, e.birth_date, e.clock_in_pin, e.created_at, e.updated_at
		FROM employees e
		LEFT JOIN roles r ON r.id = e.role_id
		LEFT JOIN departments d ON d.id = e.department_id
		WHERE e.location_id = ?
		ORDER BY e.employee_name`, locationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var employees []Employee
	for rows.Next() {
		employee, err := scanEmployee(rows)
		if err != nil {
			return nil, err
		}
		employees = append(employees, employee)
	}
	return employees, rows.Err()
}

func getEmployee(db *sql.DB, locationID int64, number string) (Employee, error) {
	row := db.QueryRow(`SELECT e.id, e.location_id, e.employee_name, e.employee_number, e.job, e.role_id, r.name, e.department_id, d.name, e.wage_rate_cents, e.wage_pay_type, e.exclude_from_labor, e.employee_status, e.location_latest_start_date, e.birth_date, e.clock_in_pin, e.created_at, e.updated_at
		FROM employees e
		LEFT JOIN roles r ON r.id = e.role_id
		LEFT JOIN departments d ON d.id = e.department_id
		WHERE e.location_id = ? AND e.employee_number = ?`, locationID, number)
	return scanEmployee(row)
}

type scanner interface {
	Scan(dest ...any) error
}

func scanEmployee(row scanner) (Employee, error) {
	var e Employee
	var created, updated string
	var birthDate sql.NullString
	var roleID sql.NullInt64
	var roleName sql.NullString
	var departmentID sql.NullInt64
	var departmentName sql.NullString
	var wageRateCents sql.NullInt64
	var wagePayType sql.NullString
	var excludeFromLabor int
	var clockInPIN sql.NullString
	err := row.Scan(&e.ID, &e.LocationID, &e.EmployeeName, &e.EmployeeNumber, &e.Job, &roleID, &roleName, &departmentID, &departmentName, &wageRateCents, &wagePayType, &excludeFromLabor, &e.EmployeeStatus, &e.LocationLatestStartDate, &birthDate, &clockInPIN, &created, &updated)
	if roleID.Valid {
		e.RoleID = &roleID.Int64
	}
	if roleName.Valid {
		e.RoleName = &roleName.String
	}
	if departmentID.Valid {
		e.DepartmentID = &departmentID.Int64
	}
	if departmentName.Valid {
		e.DepartmentName = &departmentName.String
	}
	if wageRateCents.Valid {
		e.WageRateCents = &wageRateCents.Int64
	}
	if wagePayType.Valid {
		e.WagePayType = wagePayType.String
	}
	e.ExcludeFromLabor = excludeFromLabor != 0
	if birthDate.Valid {
		e.BirthDate = &birthDate.String
	}
	if clockInPIN.Valid {
		e.ClockInPIN = &clockInPIN.String
	}
	e.CreatedAt = parseTime(created)
	e.UpdatedAt = parseTime(updated)
	return e, err
}

func employeeWageForNumber(tx *sql.Tx, employeeNumber string) (any, string, error) {
	var wageRateCents sql.NullInt64
	var wagePayType sql.NullString
	err := tx.QueryRow(`SELECT wage_rate_cents, wage_pay_type FROM employees
		WHERE employee_number = ? AND (wage_rate_cents IS NOT NULL OR wage_pay_type != '')
		ORDER BY updated_at DESC, id DESC
		LIMIT 1`, employeeNumber).Scan(&wageRateCents, &wagePayType)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var wage any
	if wageRateCents.Valid {
		wage = wageRateCents.Int64
	}
	if wagePayType.Valid {
		return wage, wagePayType.String, nil
	}
	return wage, "", nil
}

func importBio(db *sql.DB, locationID int64, file multipart.File, header *multipart.FileHeader) (ImportResult, error) {
	if header != nil && !strings.HasSuffix(strings.ToLower(header.Filename), ".xlsx") {
		return ImportResult{}, errors.New("employee bio must be an .xlsx file")
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return ImportResult{}, err
	}
	employees, err := parseBio(data)
	if err != nil {
		return ImportResult{}, err
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return ImportResult{}, err
	}
	defer tx.Rollback()
	active := map[string]BioEmployee{}
	result := ImportResult{}
	for _, employee := range employees {
		if strings.EqualFold(employee.Status, "Terminated") || employee.Number == "" {
			result.Skipped++
			continue
		}
		active[employee.Number] = employee
		var existingID int64
		err := tx.QueryRow(`SELECT id FROM employees WHERE location_id = ? AND employee_number = ?`, locationID, employee.Number).Scan(&existingID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			wageRateCents, wagePayType, err := employeeWageForNumber(tx, employee.Number)
			if err != nil {
				return ImportResult{}, err
			}
			_, err = tx.Exec(`INSERT INTO employees (location_id, employee_name, employee_number, job, wage_rate_cents, wage_pay_type, employee_status, location_latest_start_date)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, locationID, employee.Name, employee.Number, employee.Job, wageRateCents, wagePayType, "Active", employee.LatestStartDate)
			if err != nil {
				return ImportResult{}, err
			}
			result.Added++
		case err != nil:
			return ImportResult{}, err
		default:
			_, err = tx.Exec(`UPDATE employees SET employee_name = ?, job = ?, employee_status = ?, location_latest_start_date = ?, updated_at = CURRENT_TIMESTAMP
				WHERE id = ?`, employee.Name, employee.Job, "Active", employee.LatestStartDate, existingID)
			if err != nil {
				return ImportResult{}, err
			}
			result.Updated++
		}
	}
	rows, err := tx.Query(`SELECT employee_number FROM employees WHERE location_id = ?`, locationID)
	if err != nil {
		return ImportResult{}, err
	}
	var remove []string
	for rows.Next() {
		var number string
		if err := rows.Scan(&number); err != nil {
			rows.Close()
			return ImportResult{}, err
		}
		if _, ok := active[number]; !ok {
			remove = append(remove, number)
		}
	}
	rows.Close()
	for _, number := range remove {
		if _, err := tx.Exec(`DELETE FROM employees WHERE location_id = ? AND employee_number = ?`, locationID, number); err != nil {
			return ImportResult{}, err
		}
		result.Removed++
	}
	return result, tx.Commit()
}

func importBirthdays(db *sql.DB, locationID int64, file multipart.File, header *multipart.FileHeader) (ImportResult, error) {
	if header != nil && !strings.HasSuffix(strings.ToLower(header.Filename), ".xlsx") {
		return ImportResult{}, errors.New("birthday report must be an .xlsx file")
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return ImportResult{}, err
	}
	birthdays, err := parseBirthdays(data)
	if err != nil {
		return ImportResult{}, err
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return ImportResult{}, err
	}
	defer tx.Rollback()
	result := ImportResult{}
	for _, birthday := range birthdays {
		res, err := tx.Exec(`UPDATE employees SET birth_date = ?, updated_at = CURRENT_TIMESTAMP WHERE location_id = ? AND employee_name = ?`, birthday.BirthDate, locationID, birthday.Name)
		if err != nil {
			return ImportResult{}, err
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			result.Skipped++
			continue
		}
		result.Updated += int(affected)
	}
	return result, tx.Commit()
}

func importPins(db *sql.DB, locationID int64, file multipart.File, header *multipart.FileHeader) (ImportResult, error) {
	if header != nil && !strings.HasSuffix(strings.ToLower(header.Filename), ".pdf") {
		return ImportResult{}, errors.New("PIN report must be a PDF file")
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return ImportResult{}, err
	}
	pins, err := parsePinsPDF(data)
	if err != nil {
		return ImportResult{}, err
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return ImportResult{}, err
	}
	defer tx.Rollback()
	employees, err := employeePinImportIndex(tx, locationID)
	if err != nil {
		return ImportResult{}, err
	}
	result := ImportResult{}
	for _, pin := range pins {
		employeeID, ok := matchPinEmployeeID(employees, pin.Name)
		if !ok {
			result.Skipped++
			continue
		}
		res, err := tx.Exec(`UPDATE employees SET clock_in_pin = ?, updated_at = CURRENT_TIMESTAMP WHERE location_id = ? AND id = ?`, pin.ClockInPIN, locationID, employeeID)
		if err != nil {
			return ImportResult{}, err
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			result.Skipped++
			continue
		}
		result.Updated += int(affected)
	}
	return result, tx.Commit()
}

type pinImportEmployee struct {
	ID   int64
	Name string
	Keys map[string]bool
}

func employeePinImportIndex(tx *sql.Tx, locationID int64) ([]pinImportEmployee, error) {
	rows, err := tx.Query(`SELECT id, employee_name FROM employees WHERE location_id = ?`, locationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var employees []pinImportEmployee
	for rows.Next() {
		var employee pinImportEmployee
		if err := rows.Scan(&employee.ID, &employee.Name); err != nil {
			return nil, err
		}
		employee.Keys = pinNameKeys(employee.Name)
		employees = append(employees, employee)
	}
	return employees, rows.Err()
}

func matchPinEmployeeID(employees []pinImportEmployee, reportName string) (int64, bool) {
	reportKeys := pinNameKeys(reportName)
	var matchedID int64
	matches := 0
	for _, employee := range employees {
		for key := range reportKeys {
			if employee.Keys[key] {
				matchedID = employee.ID
				matches++
				break
			}
		}
		if matches > 1 {
			return 0, false
		}
	}
	if matches != 1 {
		return 0, false
	}
	return matchedID, true
}

func parseDaypartActivityPDF(file multipart.File, header *multipart.FileHeader) (DaypartSalesReport, error) {
	if header != nil && !strings.HasSuffix(strings.ToLower(header.Filename), ".pdf") {
		return DaypartSalesReport{}, errors.New("daypart activity report must be a PDF file")
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return DaypartSalesReport{}, err
	}
	text, err := extractDaypartPDFText(data)
	if err != nil {
		return DaypartSalesReport{}, err
	}
	return parseDaypartActivityText(text)
}

func extractDaypartPDFText(data []byte) (string, error) {
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err == nil {
		var text bytes.Buffer
		plain, err := reader.GetPlainText()
		if err == nil {
			if _, err := io.Copy(&text, plain); err != nil {
				return "", err
			}
			return text.String(), nil
		}
	}
	path, lookErr := exec.LookPath("pdftotext")
	if lookErr != nil {
		if err != nil {
			return "", fmt.Errorf("read PDF: %w", err)
		}
		return "", errors.New("extract PDF text: pdftotext is not installed")
	}
	tmp, err := os.CreateTemp("", "cfasuite-daypart-*.pdf")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	out, err := exec.Command(path, "-layout", tmpPath, "-").Output()
	if err != nil {
		return "", fmt.Errorf("extract PDF text with pdftotext: %w", err)
	}
	return string(out), nil
}

func parseDaypartActivityText(text string) (DaypartSalesReport, error) {
	report := DaypartSalesReport{
		StoreName:    "Unknown Store",
		Dayparts:     orderedSalesMap(salesDayparts),
		Destinations: orderedSalesMap(salesDestinations),
	}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if matches := salesStoreRe.FindStringSubmatch(line); matches != nil {
			report.StoreName = strings.TrimSpace(matches[1])
		}
		if matches := salesPeriodRe.FindStringSubmatch(line); matches != nil {
			start := formatLongDate(matches[1])
			end := formatLongDate(matches[2])
			if start == "" || end == "" {
				return DaypartSalesReport{}, errors.New("could not parse daypart activity report date range")
			}
			if start != end {
				return DaypartSalesReport{}, fmt.Errorf("daypart activity report must cover exactly one business day; got %s through %s", start, end)
			}
			report.BusinessDate = start
		}
	}
	if report.BusinessDate == "" {
		return DaypartSalesReport{}, errors.New("daypart activity report date range was not found")
	}
	lines := cleanSalesLines(text)
	daypartCandidates := map[string][]int64{}
	for _, label := range salesDayparts {
		daypartCandidates[label] = nil
	}
	collectingSummaryTotals := false
	for _, line := range lines {
		if strings.HasPrefix(line, "Report Totals:") {
			collectingSummaryTotals = true
			continue
		}
		if matches := salesDaypartRe.FindStringSubmatch(line); matches != nil {
			if candidates := extractDaypartSalesCandidates(line); len(candidates) > 0 {
				daypartCandidates[matches[1]] = candidates
			}
			continue
		}
		if collectingSummaryTotals {
			destination := salesDestinationName(line)
			if destination == "" {
				continue
			}
			if cents, ok := extractDestinationSalesValue(line, destination); ok {
				report.Destinations[destination] = cents
			}
		}
	}
	total := sumSalesMap(report.Destinations)
	if total == 0 {
		return DaypartSalesReport{}, errors.New("no destination totals were found in that PDF")
	}
	report.Dayparts = resolveDaypartSales(daypartCandidates, total)
	if sumSalesMap(report.Dayparts) == 0 {
		return DaypartSalesReport{}, errors.New("no daypart totals were found in that PDF")
	}
	return report, nil
}

func orderedSalesMap(labels []string) map[string]int64 {
	out := map[string]int64{}
	for _, label := range labels {
		out[label] = 0
	}
	return out
}

func cleanSalesLines(text string) []string {
	var lines []string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.Join(strings.Fields(raw), " ")
		if line == "" {
			continue
		}
		if salesContinuationLine(line) && len(lines) > 0 {
			lines[len(lines)-1] += line
			continue
		}
		if ignoreSalesLine(line) {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func salesContinuationLine(line string) bool {
	if salesOnlyNumberRe.MatchString(line) {
		return true
	}
	switch line {
	case "10", "01", "06", "0", "38", "14", "3":
		return true
	default:
		return false
	}
}

func ignoreSalesLine(line string) bool {
	for _, prefix := range salesNoisePrefixes {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func salesDestinationName(line string) string {
	for _, destination := range salesDestinations {
		if strings.HasPrefix(line, destination) {
			return destination
		}
	}
	return ""
}

func extractDestinationSalesValue(line, destination string) (int64, bool) {
	tail := strings.TrimSpace(strings.TrimPrefix(line, destination))
	if tail == "" {
		return 0, false
	}
	parts := strings.Fields(tail)
	if len(parts) < 2 {
		return 0, false
	}
	values := extractMoneyCents(strings.Join(parts[1:], " "))
	if len(values) == 0 {
		return 0, false
	}
	var max int64
	for _, value := range values {
		if value > max {
			max = value
		}
	}
	return max, true
}

func extractDaypartSalesCandidates(line string) []int64 {
	values := extractMoneyCents(line)
	if len(values) == 0 {
		return nil
	}
	return values[:1]
}

func extractMoneyCents(text string) []int64 {
	matches := salesMoneyRe.FindAllString(text, -1)
	values := make([]int64, 0, len(matches))
	for _, match := range matches {
		if cents, err := parseSalesMoneyCents(match); err == nil {
			values = append(values, cents)
		}
	}
	return values
}

func overlappingGroupedMoneyCents(text string) []int64 {
	matches := salesGroupedMoneyOverlapRe.FindAllStringSubmatch(text, -1)
	values := make([]int64, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			if cents, err := parseSalesMoneyCents(match[1]); err == nil {
				values = append(values, cents)
			}
		}
	}
	return values
}

func uniqueCents(values []int64) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func parseSalesMoneyCents(value string) (int64, error) {
	value = strings.ReplaceAll(strings.TrimSpace(value), ",", "")
	parts := strings.Split(value, ".")
	if len(parts) != 2 || len(parts[1]) != 2 {
		return 0, fmt.Errorf("invalid money value %q", value)
	}
	dollars, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, err
	}
	cents, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, err
	}
	return dollars*100 + cents, nil
}

func resolveDaypartSales(candidates map[string][]int64, target int64) map[string]int64 {
	out := orderedSalesMap(salesDayparts)
	var search func(int, int64, []int64) ([]int64, bool)
	search = func(idx int, sum int64, chosen []int64) ([]int64, bool) {
		if idx == len(salesDayparts) {
			return chosen, sum == target
		}
		values := candidates[salesDayparts[idx]]
		if len(values) == 0 {
			values = []int64{0}
		}
		for _, value := range values {
			if result, ok := search(idx+1, sum+value, append(chosen, value)); ok {
				return result, true
			}
		}
		return nil, false
	}
	if values, ok := search(0, 0, nil); ok {
		for i, label := range salesDayparts {
			out[label] = values[i]
		}
		return out
	}
	for _, label := range salesDayparts {
		if len(candidates[label]) > 0 {
			out[label] = candidates[label][0]
		}
	}
	return out
}

func sumSalesMap(values map[string]int64) int64 {
	var total int64
	for _, value := range values {
		total += value
	}
	return total
}

func saveDailySales(db *sql.DB, locationID int64, report DaypartSalesReport) error {
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	total := sumSalesMap(report.Destinations)
	_, err = tx.Exec(`INSERT INTO daily_sales (location_id, business_date, total_cents, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(location_id, business_date) DO UPDATE SET total_cents = excluded.total_cents, updated_at = CURRENT_TIMESTAMP`,
		locationID, report.BusinessDate, total)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM daily_sales_breakdowns WHERE location_id = ? AND business_date = ?`, locationID, report.BusinessDate); err != nil {
		return err
	}
	for _, label := range salesDayparts {
		if _, err := tx.Exec(`INSERT INTO daily_sales_breakdowns (location_id, business_date, group_type, label, cents)
			VALUES (?, ?, 'daypart', ?, ?)`, locationID, report.BusinessDate, label, report.Dayparts[label]); err != nil {
			return err
		}
	}
	for _, label := range salesDestinations {
		if _, err := tx.Exec(`INSERT INTO daily_sales_breakdowns (location_id, business_date, group_type, label, cents)
			VALUES (?, ?, 'destination', ?, ?)`, locationID, report.BusinessDate, label, report.Destinations[label]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func getDailySales(db *sql.DB, locationID int64, date string) (DailySales, error) {
	rows, err := listDailySales(db, locationID, date, date)
	if err != nil {
		return DailySales{}, err
	}
	if len(rows) == 0 {
		return DailySales{}, sql.ErrNoRows
	}
	return rows[0], nil
}

func listDailySales(db *sql.DB, locationID int64, startDate, endDate string) ([]DailySales, error) {
	rows, err := db.Query(`SELECT location_id, business_date, total_cents, created_at, updated_at
		FROM daily_sales
		WHERE location_id = ? AND business_date BETWEEN ? AND ?
		ORDER BY business_date`, locationID, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sales []DailySales
	for rows.Next() {
		var sale DailySales
		var created, updated string
		if err := rows.Scan(&sale.LocationID, &sale.BusinessDate, &sale.TotalCents, &created, &updated); err != nil {
			return nil, err
		}
		sale.CreatedAt = parseTime(created)
		sale.UpdatedAt = parseTime(updated)
		sale.Dayparts = orderedSalesMap(salesDayparts)
		sale.Destinations = orderedSalesMap(salesDestinations)
		sales = append(sales, sale)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range sales {
		if err := loadSalesBreakdowns(db, &sales[i]); err != nil {
			return nil, err
		}
	}
	return sales, nil
}

func loadSalesBreakdowns(db *sql.DB, sale *DailySales) error {
	rows, err := db.Query(`SELECT group_type, label, cents
		FROM daily_sales_breakdowns
		WHERE location_id = ? AND business_date = ?
		ORDER BY group_type, label`, sale.LocationID, sale.BusinessDate)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var groupType, label string
		var cents int64
		if err := rows.Scan(&groupType, &label, &cents); err != nil {
			return err
		}
		switch groupType {
		case "daypart":
			sale.Dayparts[label] = cents
		case "destination":
			sale.Destinations[label] = cents
		}
	}
	return rows.Err()
}

func salesDatesForCalendar(db *sql.DB, locationID int64, month time.Time) (map[string]bool, error) {
	first := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, month.Location())
	start := first.AddDate(0, 0, -int(first.Weekday())).Format("2006-01-02")
	end := first.AddDate(0, 0, -int(first.Weekday())+41).Format("2006-01-02")
	rows, err := db.Query(`SELECT business_date FROM daily_sales WHERE location_id = ? AND business_date BETWEEN ? AND ?`, locationID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	dates := map[string]bool{}
	for rows.Next() {
		var date string
		if err := rows.Scan(&date); err != nil {
			return nil, err
		}
		dates[date] = true
	}
	return dates, rows.Err()
}

func salesRangeFromRequest(r *http.Request) (time.Time, time.Time, error) {
	now := time.Now()
	startValue := strings.TrimSpace(r.URL.Query().Get("start"))
	endValue := strings.TrimSpace(r.URL.Query().Get("end"))
	if startValue == "" {
		startValue = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local).Format("2006-01-02")
	}
	if endValue == "" {
		endValue = time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.Local).Format("2006-01-02")
	}
	start, err := time.ParseInLocation("2006-01-02", startValue, time.Local)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("start must use YYYY-MM-DD format")
	}
	end, err := time.ParseInLocation("2006-01-02", endValue, time.Local)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("end must use YYYY-MM-DD format")
	}
	if end.Before(start) {
		return time.Time{}, time.Time{}, errors.New("end must be on or after start")
	}
	return start, end, nil
}

func missingSalesDates(start, end time.Time, sales []DailySales) []string {
	present := map[string]bool{}
	for _, sale := range sales {
		present[sale.BusinessDate] = true
	}
	var missing []string
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Sunday {
			continue
		}
		date := d.Format("2006-01-02")
		if !present[date] {
			missing = append(missing, date)
		}
	}
	return missing
}

func requiredSalesDateCount(start, end time.Time) int {
	count := 0
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if d.Weekday() != time.Sunday {
			count++
		}
	}
	return count
}

func salesDailyRows(sales []DailySales) []SalesDailyRow {
	rows := make([]SalesDailyRow, 0, len(sales))
	for _, sale := range sales {
		date, _ := time.ParseInLocation("2006-01-02", sale.BusinessDate, time.Local)
		rows = append(rows, SalesDailyRow{
			Date:         sale.BusinessDate,
			DateLabel:    formatISODate(sale.BusinessDate),
			Weekday:      date.Format("Monday"),
			TotalCents:   sale.TotalCents,
			Dayparts:     salesRowsForLabels(sale.Dayparts, salesDayparts),
			Destinations: salesRowsForLabels(sale.Destinations, salesDestinations),
		})
	}
	return rows
}

func aggregateSalesRows(sales []DailySales, groupType string) []SalesBreakdownRow {
	var labels []string
	totals := map[string]int64{}
	if groupType == "daypart" {
		labels = salesDayparts
		for _, sale := range sales {
			for _, label := range labels {
				totals[label] += sale.Dayparts[label]
			}
		}
	} else {
		labels = salesDestinations
		for _, sale := range sales {
			for _, label := range labels {
				totals[label] += sale.Destinations[label]
			}
		}
	}
	return salesRowsForLabels(totals, labels)
}

func dayOfWeekSalesRows(sales []DailySales) []SalesBreakdownRow {
	totals := map[string]int64{}
	for _, sale := range sales {
		date, err := time.ParseInLocation("2006-01-02", sale.BusinessDate, time.Local)
		if err != nil {
			continue
		}
		totals[date.Format("Monday")] += sale.TotalCents
	}
	return salesRowsForLabels(totals, []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"})
}

func salesRowsForLabels(values map[string]int64, labels []string) []SalesBreakdownRow {
	total := sumSalesMap(values)
	rows := make([]SalesBreakdownRow, 0, len(labels))
	for _, label := range labels {
		rows = append(rows, SalesBreakdownRow{Label: label, Cents: values[label], Percent: formatPercent(values[label], total)})
	}
	return rows
}

func saveDailyLabor(db *sql.DB, locationID int64, date string, report TimePunchReport) error {
	daily := dailyLaborFromReport(locationID, date, report)
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO daily_labor (location_id, business_date, total_minutes, overtime_minutes, total_wages_cents, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(location_id, business_date) DO UPDATE SET total_minutes = excluded.total_minutes, overtime_minutes = excluded.overtime_minutes, total_wages_cents = excluded.total_wages_cents, updated_at = CURRENT_TIMESTAMP`,
		locationID, date, daily.TotalMinutes, daily.OvertimeMinutes, daily.TotalWagesCents)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM daily_labor_breakdowns WHERE location_id = ? AND business_date = ?`, locationID, date); err != nil {
		return err
	}
	for groupType, groups := range map[string]map[string]LaborTotals{
		"role":       daily.Roles,
		"department": daily.Departments,
		"job":        daily.Jobs,
		"employee":   daily.Employees,
	} {
		for label, total := range groups {
			if _, err := tx.Exec(`INSERT INTO daily_labor_breakdowns (location_id, business_date, group_type, label, minutes, overtime_minutes, wages_cents, overtime_wages_cents)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, locationID, date, groupType, label, total.Minutes, total.OvertimeMinutes, total.WagesCents, total.OvertimeWagesCents); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func dailyLaborFromReport(locationID int64, date string, report TimePunchReport) DailyLabor {
	daily := DailyLabor{
		LocationID:   locationID,
		BusinessDate: date,
		Roles:        map[string]LaborTotals{},
		Departments:  map[string]LaborTotals{},
		Jobs:         map[string]LaborTotals{},
		Employees:    map[string]LaborTotals{},
	}
	for _, employee := range report.Employees {
		for _, day := range employee.Days {
			if day.Date != date {
				continue
			}
			total := LaborTotals{Minutes: day.Minutes, OvertimeMinutes: day.OvertimeMinutes, WagesCents: day.WagesCents, OvertimeWagesCents: day.OvertimeWagesCents}
			daily.TotalMinutes += total.Minutes
			daily.OvertimeMinutes += total.OvertimeMinutes
			daily.TotalWagesCents += total.WagesCents
			addLaborTotals(daily.Roles, laborValueOrUnassigned(employee.Role), total)
			addLaborTotals(daily.Departments, laborValueOrUnassigned(employee.Department), total)
			addLaborTotals(daily.Jobs, laborValueOrUnassigned(employee.Job), total)
			addLaborTotals(daily.Employees, laborValueOrUnassigned(employee.Name), total)
		}
	}
	return daily
}

func getDailyLabor(db *sql.DB, locationID int64, date string) (DailyLabor, error) {
	rows, err := listDailyLabor(db, locationID, date, date)
	if err != nil {
		return DailyLabor{}, err
	}
	if len(rows) == 0 {
		return DailyLabor{}, sql.ErrNoRows
	}
	return rows[0], nil
}

func listDailyLabor(db *sql.DB, locationID int64, startDate, endDate string) ([]DailyLabor, error) {
	rows, err := db.Query(`SELECT location_id, business_date, total_minutes, overtime_minutes, total_wages_cents, created_at, updated_at
		FROM daily_labor
		WHERE location_id = ? AND business_date BETWEEN ? AND ?
		ORDER BY business_date`, locationID, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var labor []DailyLabor
	for rows.Next() {
		var day DailyLabor
		var created, updated string
		if err := rows.Scan(&day.LocationID, &day.BusinessDate, &day.TotalMinutes, &day.OvertimeMinutes, &day.TotalWagesCents, &created, &updated); err != nil {
			return nil, err
		}
		day.CreatedAt = parseTime(created)
		day.UpdatedAt = parseTime(updated)
		day.Roles = map[string]LaborTotals{}
		day.Departments = map[string]LaborTotals{}
		day.Jobs = map[string]LaborTotals{}
		day.Employees = map[string]LaborTotals{}
		labor = append(labor, day)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range labor {
		if err := loadLaborBreakdowns(db, &labor[i]); err != nil {
			return nil, err
		}
	}
	return labor, nil
}

func loadLaborBreakdowns(db *sql.DB, labor *DailyLabor) error {
	rows, err := db.Query(`SELECT group_type, label, minutes, overtime_minutes, wages_cents, overtime_wages_cents
		FROM daily_labor_breakdowns
		WHERE location_id = ? AND business_date = ?
		ORDER BY group_type, label`, labor.LocationID, labor.BusinessDate)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var groupType, label string
		var total LaborTotals
		if err := rows.Scan(&groupType, &label, &total.Minutes, &total.OvertimeMinutes, &total.WagesCents, &total.OvertimeWagesCents); err != nil {
			return err
		}
		switch groupType {
		case "role":
			labor.Roles[label] = total
		case "department":
			labor.Departments[label] = total
		case "job":
			labor.Jobs[label] = total
		case "employee":
			labor.Employees[label] = total
		}
	}
	return rows.Err()
}

func laborDatesForCalendar(db *sql.DB, locationID int64, month time.Time) (map[string]bool, error) {
	first := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, month.Location())
	start := first.AddDate(0, 0, -int(first.Weekday())).Format("2006-01-02")
	end := first.AddDate(0, 0, -int(first.Weekday())+41).Format("2006-01-02")
	rows, err := db.Query(`SELECT business_date FROM daily_labor WHERE location_id = ? AND business_date BETWEEN ? AND ?`, locationID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	dates := map[string]bool{}
	for rows.Next() {
		var date string
		if err := rows.Scan(&date); err != nil {
			return nil, err
		}
		dates[date] = true
	}
	return dates, rows.Err()
}

func laborRangeFromRequest(r *http.Request) (time.Time, time.Time, error) {
	now := startOfDay(time.Now())
	defaultEnd := now.AddDate(0, 0, -1)
	startValue := strings.TrimSpace(r.URL.Query().Get("start"))
	endValue := strings.TrimSpace(r.URL.Query().Get("end"))
	if startValue == "" {
		startValue = time.Date(defaultEnd.Year(), defaultEnd.Month(), 1, 0, 0, 0, 0, time.Local).Format("2006-01-02")
	}
	if endValue == "" {
		endValue = defaultEnd.Format("2006-01-02")
	}
	start, err := time.ParseInLocation("2006-01-02", startValue, time.Local)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("start must use YYYY-MM-DD format")
	}
	end, err := time.ParseInLocation("2006-01-02", endValue, time.Local)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("end must use YYYY-MM-DD format")
	}
	if end.Before(start) {
		return time.Time{}, time.Time{}, errors.New("end must be on or after start")
	}
	if !end.Before(now) {
		return time.Time{}, time.Time{}, errors.New("labor reports can only include completed past days")
	}
	return start, end, nil
}

func missingLaborDates(start, end time.Time, labor []DailyLabor) []string {
	present := map[string]bool{}
	for _, day := range labor {
		present[day.BusinessDate] = true
	}
	var missing []string
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Sunday {
			continue
		}
		date := d.Format("2006-01-02")
		if !present[date] {
			missing = append(missing, date)
		}
	}
	return missing
}

func laborPageData(loc Location, start, end time.Time, labor []DailyLabor, missing []string, report *TimePunchReport) map[string]any {
	data := map[string]any{
		"SelectedLocation": loc,
		"StartDate":        start.Format("2006-01-02"),
		"EndDate":          end.Format("2006-01-02"),
		"MissingDates":     missing,
		"Complete":         len(missing) == 0,
		"DailyLaborRows":   laborDailyRows(labor),
		"LaborDayRows":     laborDayOfWeekRows(labor),
		"LaborRoleRows":    aggregateStoredLaborRows(labor, "role"),
		"LaborDeptRows":    aggregateStoredLaborRows(labor, "department"),
		"LaborJobRows":     aggregateStoredLaborRows(labor, "job"),
		"LaborSummary":     storedLaborSummary(labor),
	}
	if report != nil {
		data["Report"] = *report
		data["Summary"] = laborSummary(*report)
		data["DayRows"] = laborDayRows(*report)
		data["RoleRows"] = laborRoleRows(*report)
		data["DepartmentRows"] = laborDepartmentRows(*report)
		data["EmployeeRows"] = laborEmployeeRows(*report)
		data["EmployeeJobs"] = laborEmployeeJobOptions(*report)
		data["JobRows"] = laborJobRows(*report)
	}
	return data
}

func laborDailyRows(labor []DailyLabor) []LaborDailyRow {
	rows := make([]LaborDailyRow, 0, len(labor))
	for _, day := range labor {
		date, _ := time.ParseInLocation("2006-01-02", day.BusinessDate, time.Local)
		rows = append(rows, LaborDailyRow{
			Date:          day.BusinessDate,
			DateLabel:     formatISODate(day.BusinessDate),
			Weekday:       date.Format("Monday"),
			Hours:         formatHours(day.TotalMinutes),
			OvertimeHours: formatHours(day.OvertimeMinutes),
			Dollars:       formatDollars(day.TotalWagesCents),
			MinutesValue:  day.TotalMinutes,
			CentsValue:    day.TotalWagesCents,
		})
	}
	return rows
}

func laborDayOfWeekRows(labor []DailyLabor) []LaborDayRow {
	totals := map[string]LaborTotals{}
	dates := map[string]map[string]bool{}
	for _, day := range labor {
		date, err := time.ParseInLocation("2006-01-02", day.BusinessDate, time.Local)
		if err != nil {
			continue
		}
		key := date.Format("Monday")
		total := totals[key]
		total.Minutes += day.TotalMinutes
		total.OvertimeMinutes += day.OvertimeMinutes
		total.WagesCents += day.TotalWagesCents
		totals[key] = total
		if dates[key] == nil {
			dates[key] = map[string]bool{}
		}
		dates[key][day.BusinessDate] = true
	}
	grand := sumStoredLabor(labor)
	var rows []LaborDayRow
	for _, day := range []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"} {
		total := totals[day]
		rows = append(rows, LaborDayRow{
			Day:           day,
			Date:          formatDateList(dates[day]),
			Hours:         formatHours(total.Minutes),
			OvertimeHours: formatHours(total.OvertimeMinutes),
			Dollars:       formatDollars(total.WagesCents),
			Percent:       formatPercent(total.WagesCents, grand.WagesCents),
		})
	}
	return rows
}

func aggregateStoredLaborRows(labor []DailyLabor, groupType string) []LaborEmployeeRow {
	totals := map[string]LaborTotals{}
	for _, day := range labor {
		var groups map[string]LaborTotals
		switch groupType {
		case "role":
			groups = day.Roles
		case "department":
			groups = day.Departments
		default:
			groups = day.Jobs
		}
		for label, total := range groups {
			addLaborTotals(totals, label, total)
		}
	}
	grand := sumStoredLabor(labor)
	labels := make([]string, 0, len(totals))
	for label := range totals {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	rows := make([]LaborEmployeeRow, 0, len(labels))
	for _, label := range labels {
		total := totals[label]
		row := LaborEmployeeRow{
			Hours:        formatHours(total.Minutes),
			Dollars:      formatDollars(total.WagesCents),
			Percent:      formatPercent(total.WagesCents, grand.WagesCents),
			MinutesValue: total.Minutes,
			CentsValue:   total.WagesCents,
		}
		switch groupType {
		case "role":
			row.Role = label
		case "department":
			row.Department = label
		default:
			row.Job = label
		}
		rows = append(rows, row)
	}
	return rows
}

func storedLaborSummary(labor []DailyLabor) []LaborSummary {
	total := sumStoredLabor(labor)
	regularMinutes := total.Minutes - total.OvertimeMinutes
	regularWages := total.WagesCents - total.OvertimeWagesCents
	return []LaborSummary{
		{Label: "Hours", Hours: formatHours(total.Minutes), Dollars: "Regular " + formatHours(regularMinutes), Detail: "Overtime " + formatHours(total.OvertimeMinutes)},
		{Label: "Labor dollars", Hours: formatDollars(total.WagesCents), Dollars: "Regular " + formatDollars(regularWages), Detail: "Overtime " + formatDollars(total.OvertimeWagesCents)},
		{Label: "Imported days", Hours: strconv.Itoa(len(labor)), Dollars: "Completed labor days"},
	}
}

func sumStoredLabor(labor []DailyLabor) LaborTotals {
	var total LaborTotals
	for _, day := range labor {
		total.Minutes += day.TotalMinutes
		total.OvertimeMinutes += day.OvertimeMinutes
		total.WagesCents += day.TotalWagesCents
		for _, group := range day.Jobs {
			total.OvertimeWagesCents += group.OvertimeWagesCents
		}
	}
	return total
}

func addLaborTotals(values map[string]LaborTotals, label string, add LaborTotals) {
	current := values[label]
	current.Minutes += add.Minutes
	current.OvertimeMinutes += add.OvertimeMinutes
	current.WagesCents += add.WagesCents
	current.OvertimeWagesCents += add.OvertimeWagesCents
	values[label] = current
}

func laborValueOrUnassigned(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "Unassigned"
	}
	return value
}

func laborReportIncludesDate(report TimePunchReport, date string) bool {
	for _, employee := range report.Employees {
		for _, day := range employee.Days {
			if day.Date == date {
				return true
			}
		}
	}
	return false
}

func parseBio(data []byte) ([]BioEmployee, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, errors.New("workbook has no sheets")
	}
	rows, err := f.GetRows(sheets[0])
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("employee bio is empty")
	}
	headers := map[string]int{}
	for i, h := range rows[0] {
		headers[strings.TrimSpace(h)] = i
	}
	for _, col := range requiredColumns {
		if _, ok := headers[col]; !ok {
			return nil, fmt.Errorf("missing required column %q", col)
		}
	}
	var employees []BioEmployee
	for _, row := range rows[1:] {
		employee := BioEmployee{
			Name:            cell(row, headers["Employee Name"]),
			Number:          cell(row, headers["Employee Number"]),
			Job:             cell(row, headers["Job"]),
			Status:          cell(row, headers["Employee Status"]),
			LatestStartDate: cell(row, headers["Location Latest Start Date"]),
		}
		if employee.Name == "" && employee.Number == "" {
			continue
		}
		employees = append(employees, employee)
	}
	return employees, nil
}

func parseBirthdays(data []byte) ([]BirthdayEmployee, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, errors.New("workbook has no sheets")
	}
	rows, err := f.GetRows(sheets[0])
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("birthday report is empty")
	}
	headers := map[string]int{}
	for i, h := range rows[0] {
		headers[strings.TrimSpace(h)] = i
	}
	for _, col := range requiredBirthdayColumns {
		if _, ok := headers[col]; !ok {
			return nil, fmt.Errorf("missing required column %q", col)
		}
	}
	var birthdays []BirthdayEmployee
	for _, row := range rows[1:] {
		name := cell(row, headers["Employee Name"])
		birthDate := normalizeDate(cell(row, headers["Birth Date"]))
		if name == "" || birthDate == "" {
			continue
		}
		birthdays = append(birthdays, BirthdayEmployee{Name: name, BirthDate: birthDate})
	}
	return birthdays, nil
}

func parsePinsPDF(data []byte) ([]PinEmployee, error) {
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("read PDF: %w", err)
	}
	var text bytes.Buffer
	plain, err := reader.GetPlainText()
	if err != nil {
		return nil, fmt.Errorf("extract PDF text: %w", err)
	}
	if _, err := io.Copy(&text, plain); err != nil {
		return nil, err
	}
	return parsePinsText(text.String())
}

func parsePinsText(text string) ([]PinEmployee, error) {
	lines := cleanPinLines(text)
	var pins []PinEmployee
	for i := 0; i < len(lines); i++ {
		name := lines[i]
		if isPinHeader(name) || isPinValue(name) {
			continue
		}
		if i+2 >= len(lines) || !isPinGroupLine(lines[i+1]) || !isPinValue(lines[i+2]) {
			continue
		}
		employee := PinEmployee{Name: name, ClockInPIN: lines[i+2]}
		i += 2
		if i+1 < len(lines) && isPinValue(lines[i+1]) {
			i++
		}
		pins = append(pins, employee)
	}
	if len(pins) == 0 {
		return nil, errors.New("no employee PIN rows found")
	}
	return pins, nil
}

func pinNameKeys(name string) map[string]bool {
	keys := map[string]bool{}
	all := compactNameKey(nameTokens(name))
	if all != "" {
		keys[all] = true
	}
	noInitials := compactNameKey(removeInitialTokens(nameTokens(name)))
	if noInitials != "" {
		keys[noInitials] = true
	}
	last, given, ok := splitCommaName(name)
	if !ok {
		return keys
	}
	lastTokens := removeInitialTokens(nameTokens(last))
	givenWithoutNickname := removeParenthetical(given)
	givenTokens := removeInitialTokens(nameTokens(givenWithoutNickname))
	nicknameTokens := removeInitialTokens(parentheticalNameTokens(given))
	addPinNameKey(keys, appendNameTokens(lastTokens, givenTokens...))
	addPinNameKey(keys, appendNameTokens(appendNameTokens(lastTokens, givenTokens...), nicknameTokens...))
	if len(givenTokens) > 0 {
		addPinNameKey(keys, appendNameTokens(lastTokens, givenTokens[0]))
		addPinNameKey(keys, appendNameTokens(appendNameTokens(lastTokens, givenTokens[0]), nicknameTokens...))
	}
	return keys
}

func addPinNameKey(keys map[string]bool, tokens []string) {
	if key := compactNameKey(tokens); key != "" {
		keys[key] = true
	}
}

func splitCommaName(name string) (string, string, bool) {
	parts := strings.SplitN(name, ",", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

func nameTokens(value string) []string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, ".", " ")
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		b.WriteByte(' ')
	}
	return strings.Fields(b.String())
}

func parentheticalNameTokens(value string) []string {
	matches := parentheticalNameRe.FindAllStringSubmatch(value, -1)
	var tokens []string
	for _, match := range matches {
		if len(match) > 1 {
			tokens = append(tokens, nameTokens(match[1])...)
		}
	}
	return tokens
}

func removeParenthetical(value string) string {
	return parentheticalNameRe.ReplaceAllString(value, " ")
}

func removeInitialTokens(tokens []string) []string {
	filtered := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if len(token) == 1 {
			continue
		}
		filtered = append(filtered, token)
	}
	return filtered
}

func appendNameTokens(tokens []string, values ...string) []string {
	combined := make([]string, 0, len(tokens)+len(values))
	combined = append(combined, tokens...)
	combined = append(combined, values...)
	return combined
}

func compactNameKey(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	return strings.Join(tokens, " ")
}

func cleanPinLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.ReplaceAll(line, "\f", " ")
		cleaned := strings.Join(strings.Fields(line), " ")
		if cleaned != "" {
			lines = append(lines, cleaned)
		}
	}
	return lines
}

func isPinHeader(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "full name", "employee group", "clock-in pin", "sign-in pin":
		return true
	default:
		return false
	}
}

func isPinGroupLine(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !isPinHeader(value) && !isPinValue(value)
}

func isPinValue(value string) bool {
	if len(value) < 4 || len(value) > 8 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func parseTimePunchPDF(file multipart.File, header *multipart.FileHeader) (TimePunchReport, error) {
	if header != nil && !strings.HasSuffix(strings.ToLower(header.Filename), ".pdf") {
		return TimePunchReport{}, errors.New("time punch report must be a PDF file")
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return TimePunchReport{}, err
	}
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return TimePunchReport{}, fmt.Errorf("read PDF: %w", err)
	}
	var text bytes.Buffer
	plain, err := reader.GetPlainText()
	if err != nil {
		return TimePunchReport{}, fmt.Errorf("extract PDF text: %w", err)
	}
	if _, err := io.Copy(&text, plain); err != nil {
		return TimePunchReport{}, err
	}
	return parseTimePunchText(text.String())
}

func timePunchReportFromRequest(w http.ResponseWriter, r *http.Request) (TimePunchReport, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		return TimePunchReport{}, err
	}
	file, header, err := r.FormFile("time_punch")
	if err != nil {
		return TimePunchReport{}, errors.New("time punch report PDF is required")
	}
	defer file.Close()
	return parseTimePunchPDF(file, header)
}

func parseTimePunchText(text string) (TimePunchReport, error) {
	report := TimePunchReport{Title: "Employee Time Detail"}
	var current *LaborEmployee
	expectLocation := false
	for _, line := range cleanReportLines(text) {
		switch {
		case line == "Employee Time Detail":
			report.Title = line
			expectLocation = true
			continue
		case expectLocation && !strings.HasPrefix(line, "From "):
			report.LocationName = line
			expectLocation = false
			continue
		case strings.HasPrefix(line, "From "):
			report.PeriodLabel = line
			report.StartDate, report.EndDate = parseReportPeriod(line)
			expectLocation = false
			continue
		}
		expectLocation = false
		if matches := employeeTotalsRe.FindStringSubmatch(line); matches != nil {
			if current != nil {
				current.Totals = parseLaborTotals(matches[1], matches[2])
			}
			continue
		}
		if matches := grandTotalsRe.FindStringSubmatch(line); matches != nil {
			report.GrandTotals = parseLaborTotals(matches[1], matches[2])
			continue
		}
		if ignoreTimePunchLine(line) {
			continue
		}
		if employeeNameRe.MatchString(line) && strings.Contains(line, ",") {
			report.Employees = append(report.Employees, LaborEmployee{Name: line})
			current = &report.Employees[len(report.Employees)-1]
			continue
		}
		matches := punchLineRe.FindStringSubmatch(line)
		if matches == nil || current == nil {
			continue
		}
		if wageRate := firstMoneyCents(matches[5]); wageRate > 0 {
			current.WageRateCents = wageRate
			current.WagePayType = wagePayTypeForRate(wageRate)
		}
		payType := strings.ToLower(matches[4])
		minutes := 0
		overtimeMinutes := 0
		wagesCents := punchWagesCents(matches[5])
		overtimeWagesCents := int64(0)
		switch payType {
		case "regular":
			minutes = parseDurationMinutes(matches[3])
		case "overtime":
			overtimeMinutes = parseDurationMinutes(matches[3])
			minutes = overtimeMinutes
			overtimeWagesCents = wagesCents
		}
		day := LaborDay{
			Weekday:            titleWeekday(matches[1]),
			Date:               normalizeUSDate(matches[2]),
			Minutes:            minutes,
			OvertimeMinutes:    overtimeMinutes,
			WagesCents:         wagesCents,
			OvertimeWagesCents: overtimeWagesCents,
		}
		addLaborDay(current, day)
	}
	for i := range report.Employees {
		dayTotals := sumEmployeeDays(report.Employees[i])
		if report.Employees[i].Totals.Minutes == 0 && report.Employees[i].Totals.WagesCents == 0 {
			report.Employees[i].Totals = dayTotals
		} else {
			if report.Employees[i].Totals.OvertimeMinutes == 0 {
				report.Employees[i].Totals.OvertimeMinutes = dayTotals.OvertimeMinutes
			}
			if report.Employees[i].Totals.OvertimeWagesCents == 0 {
				report.Employees[i].Totals.OvertimeWagesCents = dayTotals.OvertimeWagesCents
			}
		}
	}
	if report.GrandTotals.Minutes == 0 && report.GrandTotals.WagesCents == 0 {
		for _, employee := range report.Employees {
			report.GrandTotals.Minutes += employee.Totals.Minutes
			report.GrandTotals.OvertimeMinutes += employee.Totals.OvertimeMinutes
			report.GrandTotals.WagesCents += employee.Totals.WagesCents
			report.GrandTotals.OvertimeWagesCents += employee.Totals.OvertimeWagesCents
		}
	} else if report.GrandTotals.OvertimeMinutes == 0 || report.GrandTotals.OvertimeWagesCents == 0 {
		var overtimeMinutes int
		var overtimeWagesCents int64
		for _, employee := range report.Employees {
			overtimeMinutes += employee.Totals.OvertimeMinutes
			overtimeWagesCents += employee.Totals.OvertimeWagesCents
		}
		if report.GrandTotals.OvertimeMinutes == 0 {
			report.GrandTotals.OvertimeMinutes = overtimeMinutes
		}
		if report.GrandTotals.OvertimeWagesCents == 0 {
			report.GrandTotals.OvertimeWagesCents = overtimeWagesCents
		}
	}
	return report, nil
}

func cleanReportLines(text string) []string {
	text = normalizeTimePunchText(text)
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.ReplaceAll(line, "\f", " ")
		cleaned := strings.Join(strings.Fields(line), " ")
		if cleaned != "" {
			lines = append(lines, cleaned)
		}
	}
	return lines
}

func normalizeTimePunchText(text string) string {
	compact := strings.Join(strings.Fields(strings.ReplaceAll(text, "\f", " ")), " ")
	if compact == "" {
		return ""
	}
	compact = strings.ReplaceAll(compact, "Employee Time Detail", "\nEmployee Time Detail\n")
	compact = reportHeaderRe.ReplaceAllString(compact, "\n")
	compact = strings.ReplaceAll(compact, "Punch types of", "\nPunch types of")
	compact = fromPeriodInlineRe.ReplaceAllString(compact, "$1\nFrom $2")
	compact = periodThenEmployeeRe.ReplaceAllString(compact, "$1\n$2")
	compact = employeeTotalsInlineRe.ReplaceAllString(compact, "\nEmployee Totals")
	compact = grandTotalsInlineRe.ReplaceAllString(compact, "\nAll Employees Grand Total")
	compact = moneyThenEmployeeRe.ReplaceAllString(compact, "$1\n$2")
	compact = weekdayInlineRe.ReplaceAllString(compact, "\n$1, ")
	return compact
}

func ignoreTimePunchLine(line string) bool {
	return line == "Employee" ||
		line == "Date" ||
		line == "Name" ||
		line == "Time In" ||
		line == "Time Out" ||
		line == "Total Time" ||
		line == "Pay Type" ||
		line == "Wage Rate" ||
		line == "Regular Hours Wages Overtime Hours Wages Total Wages" ||
		strings.HasPrefix(line, "Punch types of") ||
		strings.HasPrefix(line, "* - clock-in time or clock-out time") ||
		strings.Contains(line, "Page ") ||
		footerTimestampRe.MatchString(line)
}

func addLaborDay(employee *LaborEmployee, day LaborDay) {
	for i := range employee.Days {
		if employee.Days[i].Date == day.Date {
			employee.Days[i].Minutes += day.Minutes
			employee.Days[i].OvertimeMinutes += day.OvertimeMinutes
			employee.Days[i].WagesCents += day.WagesCents
			employee.Days[i].OvertimeWagesCents += day.OvertimeWagesCents
			return
		}
	}
	employee.Days = append(employee.Days, day)
	sort.Slice(employee.Days, func(i, j int) bool { return employee.Days[i].Date < employee.Days[j].Date })
}

func sumEmployeeDays(employee LaborEmployee) LaborTotals {
	var totals LaborTotals
	for _, day := range employee.Days {
		totals.Minutes += day.Minutes
		totals.OvertimeMinutes += day.OvertimeMinutes
		totals.WagesCents += day.WagesCents
		totals.OvertimeWagesCents += day.OvertimeWagesCents
	}
	return totals
}

func applyEmployeeAssignments(report *TimePunchReport, employees []Employee) {
	type assignment struct {
		number           string
		job              string
		role             string
		department       string
		wageRateCents    int64
		wagePayType      string
		excludeFromLabor bool
	}
	byName := map[string]assignment{}
	for _, employee := range employees {
		current := assignment{
			number:           employee.EmployeeNumber,
			job:              strings.TrimSpace(employee.Job),
			role:             "Unassigned",
			department:       "Unassigned",
			wagePayType:      strings.TrimSpace(employee.WagePayType),
			excludeFromLabor: employee.ExcludeFromLabor,
		}
		if employee.WageRateCents != nil {
			current.wageRateCents = *employee.WageRateCents
		}
		if employee.RoleName != nil && strings.TrimSpace(*employee.RoleName) != "" {
			current.role = strings.TrimSpace(*employee.RoleName)
		}
		if employee.DepartmentName != nil && strings.TrimSpace(*employee.DepartmentName) != "" {
			current.department = strings.TrimSpace(*employee.DepartmentName)
		}
		byName[normalizeName(employee.EmployeeName)] = current
	}
	for i := range report.Employees {
		if assignment, ok := byName[normalizeName(report.Employees[i].Name)]; ok {
			report.Employees[i].EmployeeNumber = assignment.number
			report.Employees[i].Job = assignment.job
			if report.Employees[i].Job == "" {
				report.Employees[i].Job = "Unmatched"
			}
			report.Employees[i].Role = assignment.role
			report.Employees[i].Department = assignment.department
			if assignment.wageRateCents > 0 {
				report.Employees[i].WageRateCents = assignment.wageRateCents
				report.Employees[i].WagePayType = assignment.wagePayType
			}
			report.Employees[i].ExcludeFromLabor = assignment.excludeFromLabor
		} else {
			report.Employees[i].Job = "Unmatched"
			report.Employees[i].Role = "Unmatched"
			report.Employees[i].Department = "Unmatched"
		}
	}
}

func applyEmployeeJobs(report *TimePunchReport, employees []Employee) {
	applyEmployeeAssignments(report, employees)
}

func updateEmployeeWagesFromReport(db *sql.DB, report TimePunchReport, employees []Employee) error {
	byName := map[string]Employee{}
	for _, employee := range employees {
		byName[normalizeName(employee.EmployeeName)] = employee
	}
	for _, laborEmployee := range report.Employees {
		if laborEmployee.WageRateCents <= 0 {
			continue
		}
		employee, ok := byName[normalizeName(laborEmployee.Name)]
		if !ok {
			continue
		}
		if _, err := db.Exec(`UPDATE employees SET wage_rate_cents = ?, wage_pay_type = ?, updated_at = CURRENT_TIMESTAMP WHERE employee_number = ?`, laborEmployee.WageRateCents, wagePayTypeForRate(laborEmployee.WageRateCents), employee.EmployeeNumber); err != nil {
			return err
		}
	}
	return nil
}

func finalizeLaborReport(report *TimePunchReport, employees []Employee) {
	addMissingSalaryEmployees(report, employees)
	applySalaryLabor(report)
	filterExcludedLabor(report)
	recalculateReportTotals(report)
}

func addMissingSalaryEmployees(report *TimePunchReport, employees []Employee) {
	seen := map[string]bool{}
	for _, employee := range report.Employees {
		if employee.EmployeeNumber != "" {
			seen[employee.EmployeeNumber] = true
		}
	}
	for _, employee := range employees {
		if employee.WagePayType != "salary" || employee.WageRateCents == nil || employee.ExcludeFromLabor || seen[employee.EmployeeNumber] {
			continue
		}
		role := "Unassigned"
		if employee.RoleName != nil && strings.TrimSpace(*employee.RoleName) != "" {
			role = strings.TrimSpace(*employee.RoleName)
		}
		department := "Unassigned"
		if employee.DepartmentName != nil && strings.TrimSpace(*employee.DepartmentName) != "" {
			department = strings.TrimSpace(*employee.DepartmentName)
		}
		report.Employees = append(report.Employees, LaborEmployee{
			Name:           employee.EmployeeName,
			EmployeeNumber: employee.EmployeeNumber,
			Job:            employee.Job,
			Role:           role,
			Department:     department,
			WageRateCents:  *employee.WageRateCents,
			WagePayType:    employee.WagePayType,
		})
	}
}

func applySalaryLabor(report *TimePunchReport) {
	for i := range report.Employees {
		employee := &report.Employees[i]
		if employee.WagePayType != "salary" || employee.WageRateCents <= 0 {
			continue
		}
		employee.Days = mergeSalaryLaborDays(employee.Days, salaryLaborDays(report.StartDate, report.EndDate, employee.WageRateCents))
		employee.Totals = sumEmployeeDays(*employee)
	}
}

func mergeSalaryLaborDays(punchedDays, salaryDays []LaborDay) []LaborDay {
	byDate := map[string]int{}
	merged := append([]LaborDay(nil), punchedDays...)
	for i, day := range merged {
		byDate[day.Date] = i
	}
	for _, salaryDay := range salaryDays {
		if index, ok := byDate[salaryDay.Date]; ok {
			merged[index].WagesCents = salaryDay.WagesCents
			continue
		}
		byDate[salaryDay.Date] = len(merged)
		merged = append(merged, salaryDay)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Date < merged[j].Date })
	return merged
}

func filterExcludedLabor(report *TimePunchReport) {
	employees := report.Employees[:0]
	for _, employee := range report.Employees {
		if !employee.ExcludeFromLabor {
			employees = append(employees, employee)
		}
	}
	report.Employees = employees
}

func recalculateReportTotals(report *TimePunchReport) {
	report.GrandTotals = LaborTotals{}
	for _, employee := range report.Employees {
		report.GrandTotals.Minutes += employee.Totals.Minutes
		report.GrandTotals.OvertimeMinutes += employee.Totals.OvertimeMinutes
		report.GrandTotals.WagesCents += employee.Totals.WagesCents
		report.GrandTotals.OvertimeWagesCents += employee.Totals.OvertimeWagesCents
	}
}

func salaryLaborDays(startDate, endDate string, monthlyCents int64) []LaborDay {
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return nil
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil || end.Before(start) {
		end = start
	}
	var days []LaborDay
	monthCounts := map[string]int{}
	for current := start; !current.After(end); current = current.AddDate(0, 0, 1) {
		_, month, _ := current.Date()
		daysInMonth := time.Date(current.Year(), month+1, 0, 0, 0, 0, 0, current.Location()).Day()
		monthKey := current.Format("2006-01")
		monthCounts[monthKey]++
		wagesCents := monthlyCents / int64(daysInMonth)
		if int64(monthCounts[monthKey]) <= monthlyCents%int64(daysInMonth) {
			wagesCents++
		}
		days = append(days, LaborDay{
			Weekday:    current.Weekday().String(),
			Date:       current.Format("2006-01-02"),
			WagesCents: wagesCents,
		})
	}
	return days
}

func laborSummary(report TimePunchReport) []LaborSummary {
	regularMinutes := report.GrandTotals.Minutes - report.GrandTotals.OvertimeMinutes
	if report.GrandTotals.RegularMinutes > 0 {
		regularMinutes = report.GrandTotals.RegularMinutes
	}
	regularWages := report.GrandTotals.WagesCents - report.GrandTotals.OvertimeWagesCents
	if report.GrandTotals.RegularWagesCents > 0 {
		regularWages = report.GrandTotals.RegularWagesCents
	}
	return []LaborSummary{
		{Label: "Total week", Hours: formatHours(report.GrandTotals.Minutes), Dollars: "Regular " + formatHours(regularMinutes), Detail: "Overtime " + formatHours(report.GrandTotals.OvertimeMinutes)},
		{Label: "Labor dollars", Hours: formatDollars(report.GrandTotals.WagesCents), Dollars: "Regular " + formatDollars(regularWages), Detail: "Overtime " + formatDollars(report.GrandTotals.OvertimeWagesCents)},
		{Label: "Employees", Hours: strconv.Itoa(len(report.Employees)), Dollars: report.PeriodLabel},
	}
}

func laborDayRows(report TimePunchReport) []LaborDayRow {
	totals := map[string]LaborDay{}
	dates := map[string]map[string]bool{}
	for _, employee := range report.Employees {
		for _, day := range employee.Days {
			key := day.Weekday
			total := totals[key]
			if total.Weekday == "" {
				total.Weekday = day.Weekday
			}
			if dates[key] == nil {
				dates[key] = map[string]bool{}
			}
			if day.Date != "" {
				dates[key][day.Date] = true
			}
			total.Minutes += day.Minutes
			total.OvertimeMinutes += day.OvertimeMinutes
			total.WagesCents += day.WagesCents
			totals[key] = total
		}
	}
	keys := make([]string, 0, len(totals))
	for key := range totals {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return weekdayIndex(keys[i]) < weekdayIndex(keys[j]) })
	rows := make([]LaborDayRow, 0, len(keys))
	for _, key := range keys {
		total := totals[key]
		rows = append(rows, LaborDayRow{
			Day:           total.Weekday,
			Date:          formatDateList(dates[key]),
			Hours:         formatHours(total.Minutes),
			OvertimeHours: formatHours(total.OvertimeMinutes),
			Dollars:       formatDollars(total.WagesCents),
			Percent:       formatPercent(total.WagesCents, report.GrandTotals.WagesCents),
		})
	}
	return rows
}

func laborEmployeeRows(report TimePunchReport) []LaborEmployeeRow {
	employees := append([]LaborEmployee(nil), report.Employees...)
	sort.Slice(employees, func(i, j int) bool {
		if employees[i].Totals.Minutes == employees[j].Totals.Minutes {
			return employees[i].Name < employees[j].Name
		}
		return employees[i].Totals.Minutes > employees[j].Totals.Minutes
	})
	rows := make([]LaborEmployeeRow, 0, len(employees))
	for _, employee := range employees {
		rows = append(rows, LaborEmployeeRow{
			Name:         employee.Name,
			Job:          employee.Job,
			Role:         employee.Role,
			Department:   employee.Department,
			Hours:        formatHours(employee.Totals.Minutes),
			Dollars:      formatDollars(employee.Totals.WagesCents),
			MinutesValue: employee.Totals.Minutes,
			CentsValue:   employee.Totals.WagesCents,
		})
	}
	return rows
}

func laborEmployeeJobOptions(report TimePunchReport) []string {
	seen := map[string]bool{}
	for _, employee := range report.Employees {
		job := employee.Job
		if job == "" {
			job = "Unmatched"
		}
		seen[job] = true
	}
	jobs := make([]string, 0, len(seen))
	for job := range seen {
		jobs = append(jobs, job)
	}
	sort.Strings(jobs)
	return jobs
}

func employeeJobOptions(employees []Employee) []string {
	seen := map[string]bool{}
	for _, employee := range employees {
		job := strings.TrimSpace(employee.Job)
		if job != "" {
			seen[job] = true
		}
	}
	jobs := make([]string, 0, len(seen))
	for job := range seen {
		jobs = append(jobs, job)
	}
	sort.Strings(jobs)
	return jobs
}

func employeeAssignmentStatus(employees []Employee) AssignmentStatus {
	var status AssignmentStatus
	for _, employee := range employees {
		if employee.RoleID == nil {
			status.RoleUnassigned++
		}
		if employee.DepartmentID == nil {
			status.DepartmentUnassigned++
		}
	}
	return status
}

func laborJobRows(report TimePunchReport) []LaborEmployeeRow {
	return laborGroupRows(report, "job")
}

func laborRoleRows(report TimePunchReport) []LaborEmployeeRow {
	return laborGroupRows(report, "role")
}

func laborDepartmentRows(report TimePunchReport) []LaborEmployeeRow {
	return laborGroupRows(report, "department")
}

func laborGroupRows(report TimePunchReport, group string) []LaborEmployeeRow {
	type total struct {
		minutes       int
		cents         int64
		overtimeCents int64
	}
	byGroup := map[string]total{}
	for _, employee := range report.Employees {
		key := laborGroupValue(employee, group)
		current := byGroup[key]
		current.minutes += employee.Totals.Minutes
		current.cents += employee.Totals.WagesCents
		current.overtimeCents += employee.Totals.OvertimeWagesCents
		byGroup[key] = current
	}
	type groupRow struct {
		row   LaborEmployeeRow
		cents int64
		key   string
	}
	sortable := make([]groupRow, 0, len(byGroup))
	for key, total := range byGroup {
		row := LaborEmployeeRow{
			Hours:        formatHours(total.minutes),
			Dollars:      formatDollars(total.cents),
			Percent:      formatPercent(total.cents, report.GrandTotals.WagesCents),
			MinutesValue: total.minutes,
			CentsValue:   total.cents,
		}
		switch group {
		case "role":
			row.Role = key
		case "department":
			row.Department = key
		default:
			row.Job = key
		}
		sortable = append(sortable, groupRow{row: row, cents: total.cents, key: key})
	}
	sort.Slice(sortable, func(i, j int) bool {
		if sortable[i].cents == sortable[j].cents {
			return sortable[i].key < sortable[j].key
		}
		return sortable[i].cents > sortable[j].cents
	})
	rows := make([]LaborEmployeeRow, 0, len(sortable))
	for _, item := range sortable {
		rows = append(rows, item.row)
	}
	return rows
}

func laborGroupValue(employee LaborEmployee, group string) string {
	var value string
	switch group {
	case "role":
		value = employee.Role
	case "department":
		value = employee.Department
	default:
		value = employee.Job
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "Unmatched"
	}
	return value
}

func parseReportPeriod(line string) (string, string) {
	matches := periodRe.FindStringSubmatch(line)
	if matches == nil {
		return "", ""
	}
	return formatLongDate(matches[1]), formatLongDate(matches[2])
}

func parseDurationMinutes(value string) int {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0
	}
	hours, _ := strconv.Atoi(parts[0])
	minutes, _ := strconv.Atoi(parts[1])
	return hours*60 + minutes
}

func parseLaborTotals(totalDuration, text string) LaborTotals {
	totals := LaborTotals{
		Minutes:    parseDurationMinutes(totalDuration),
		WagesCents: lastMoneyCents(text),
	}
	if matches := laborTotalsDetailRe.FindStringSubmatch(text); matches != nil {
		totals.RegularMinutes = parseDurationMinutes(matches[1])
		totals.RegularWagesCents = parseMoneyCents(matches[2])
		totals.OvertimeMinutes = parseDurationMinutes(matches[3])
		totals.OvertimeWagesCents = parseMoneyCents(matches[4])
		totals.WagesCents = parseMoneyCents(matches[5])
		if totals.RegularMinutes > 0 && totals.RegularMinutes+totals.OvertimeMinutes != totals.Minutes {
			totals.OvertimeMinutes = totals.Minutes - totals.RegularMinutes
		}
		return totals
	}
	durations := durationRe.FindAllString(text, -1)
	if len(durations) >= 2 {
		totals.RegularMinutes = parseDurationMinutes(durations[0])
		totals.OvertimeMinutes = parseDurationMinutes(durations[1])
	}
	matches := moneyRe.FindAllString(text, -1)
	if len(matches) >= 3 {
		totals.RegularWagesCents = parseMoneyCents(matches[0])
		totals.OvertimeWagesCents = parseMoneyCents(matches[1])
	}
	if totals.RegularMinutes > 0 && totals.RegularMinutes+totals.OvertimeMinutes != totals.Minutes {
		totals.OvertimeMinutes = totals.Minutes - totals.RegularMinutes
	}
	return totals
}

func lastMoneyCents(text string) int64 {
	matches := moneyRe.FindAllString(text, -1)
	if len(matches) == 0 {
		return 0
	}
	return parseMoneyCents(matches[len(matches)-1])
}

func firstMoneyCents(text string) int64 {
	matches := moneyRe.FindAllString(text, -1)
	if len(matches) == 0 {
		return 0
	}
	return parseMoneyCents(matches[0])
}

func punchWagesCents(text string) int64 {
	matches := moneyRe.FindAllString(text, -1)
	if len(matches) < 2 {
		return 0
	}
	return parseMoneyCents(matches[len(matches)-1])
}

func wagePayTypeForRate(cents int64) string {
	if cents > 10000 {
		return "salary"
	}
	return "hourly"
}

func parseMoneyCents(value string) int64 {
	normalized := strings.TrimPrefix(strings.ReplaceAll(value, ",", ""), "$")
	parts := strings.SplitN(normalized, ".", 2)
	dollars, _ := strconv.ParseInt(parts[0], 10, 64)
	var cents int64
	if len(parts) == 2 {
		fraction := parts[1]
		if len(fraction) > 2 {
			fraction = fraction[:2]
		}
		for len(fraction) < 2 {
			fraction += "0"
		}
		cents, _ = strconv.ParseInt(fraction, 10, 64)
	}
	return dollars*100 + cents
}

func parseWageCents(value string) (int64, error) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.ReplaceAll(value, ",", ""), "$"))
	if value == "" {
		return 0, errors.New("wage amount is required")
	}
	parts := strings.SplitN(value, ".", 2)
	dollars, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || dollars < 0 {
		return 0, errors.New("wage amount must be a positive dollar amount")
	}
	var cents int64
	if len(parts) == 2 {
		fraction := parts[1]
		if len(fraction) > 2 {
			return 0, errors.New("wage amount can only include cents")
		}
		for len(fraction) < 2 {
			fraction += "0"
		}
		cents, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil || cents < 0 {
			return 0, errors.New("wage amount must be a positive dollar amount")
		}
	}
	total := dollars*100 + cents
	if total <= 0 {
		return 0, errors.New("wage amount must be greater than zero")
	}
	return total, nil
}

func normalizeUSDate(value string) string {
	if t, err := time.Parse("01/02/2006", value); err == nil {
		return t.Format("2006-01-02")
	}
	return value
}

func formatLongDate(value string) string {
	for _, layout := range []string{"Monday, January 2, 2006", "Monday, Jan 2, 2006", "Monday, Jan 02, 2006"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return value
}

func formatISODate(value string) string {
	date, err := time.ParseInLocation("2006-01-02", value, time.Local)
	if err != nil {
		return value
	}
	return date.Format("Monday, January 2, 2006")
}

func calendarDayPath(locationID int64, date string) string {
	return fmt.Sprintf("/locations/%d/calendar/%s", locationID, date)
}

func titleWeekday(value string) string {
	switch strings.ToLower(value) {
	case "mon":
		return "Monday"
	case "tue":
		return "Tuesday"
	case "wed":
		return "Wednesday"
	case "thu":
		return "Thursday"
	case "fri":
		return "Friday"
	case "sat":
		return "Saturday"
	case "sun":
		return "Sunday"
	default:
		return value
	}
}

func normalizeName(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}

func formatHours(minutes int) string {
	return fmt.Sprintf("%.2f", float64(minutes)/60)
}

func formatPercent(part, total int64) string {
	if total == 0 {
		return "0.0%"
	}
	return fmt.Sprintf("%.1f%%", float64(part)*100/float64(total))
}

func formatDollars(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s$%d.%02d", sign, cents/100, cents%100)
}

func weekdayIndex(weekday string) int {
	switch weekday {
	case "Sunday":
		return 0
	case "Monday":
		return 1
	case "Tuesday":
		return 2
	case "Wednesday":
		return 3
	case "Thursday":
		return 4
	case "Friday":
		return 5
	case "Saturday":
		return 6
	default:
		return 7
	}
}

func formatDateList(values map[string]bool) string {
	dates := make([]string, 0, len(values))
	for value := range values {
		dates = append(dates, value)
	}
	sort.Strings(dates)
	return strings.Join(dates, ", ")
}

func cell(row []string, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[idx])
}

func normalizeDate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, layout := range []string{
		"2006-01-02",
		"1/2/2006",
		"01/02/2006",
		"1/2/06",
		"01/02/06",
		"2006/01/02",
		"1-2-2006",
		"01-02-2006",
	} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return value
}

func createToken(db *sql.DB, name string) (string, Token, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", Token{}, errors.New("token name is required")
	}
	raw := "cfa_" + randomToken(32)
	hash := tokenHash(raw)
	prefix := raw
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	res, err := db.Exec(`INSERT INTO api_tokens (name, token_hash, prefix) VALUES (?, ?, ?)`, name, hash, prefix)
	if err != nil {
		return "", Token{}, err
	}
	id, _ := res.LastInsertId()
	return raw, Token{ID: id, Name: name, Prefix: prefix, CreatedAt: time.Now()}, nil
}

func listTokens(db *sql.DB) ([]Token, error) {
	rows, err := db.Query(`SELECT id, name, prefix, created_at, last_used_at FROM api_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tokens []Token
	for rows.Next() {
		var token Token
		var created string
		var last sql.NullString
		if err := rows.Scan(&token.ID, &token.Name, &token.Prefix, &created, &last); err != nil {
			return nil, err
		}
		token.CreatedAt = parseTime(created)
		if last.Valid {
			token.LastUsedAt = &last.String
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

func validToken(db *sql.DB, raw string) bool {
	hash := tokenHash(raw)
	res, err := db.Exec(`UPDATE api_tokens SET last_used_at = CURRENT_TIMESTAMP WHERE token_hash = ?`, hash)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

func tokenHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func purgeOldAttempts(db *sql.DB) error {
	_, err := db.Exec(`DELETE FROM login_attempts WHERE last_attempt_at < ?`, time.Now().Add(-banWindow).UTC().Format(time.RFC3339))
	return err
}

func isBanned(db *sql.DB, ip string) (bool, error) {
	var banned int
	err := db.QueryRow(`SELECT banned FROM login_attempts WHERE ip = ?`, ip).Scan(&banned)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return banned == 1, err
}

func recordFailedAttempt(db *sql.DB, ip string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(`INSERT INTO login_attempts (ip, attempts, banned, last_attempt_at) VALUES (?, 1, 0, ?)
		ON CONFLICT(ip) DO UPDATE SET attempts = attempts + 1, banned = CASE WHEN attempts + 1 >= ? THEN 1 ELSE banned END, last_attempt_at = ?`,
		ip, now, maxLoginFails, now)
	return err
}

func clearAttempt(db *sql.DB, ip string) error {
	_, err := db.Exec(`DELETE FROM login_attempts WHERE ip = ?`, ip)
	return err
}

func setSetting(db *sql.DB, key, value string) error {
	_, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`, key, value)
	return err
}

func getSetting(db *sql.DB, key string) (string, error) {
	var value string
	err := db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

func apiContext(baseURL string) string {
	return fmt.Sprintf(`# cfasuite-hr API Context

Base URL: %s

Purpose:
cfasuite-hr exposes Chick-fil-A restaurant locations and active employee records to other systems.
Employee records come from uploaded employee bio .xlsx files. Birthdays come from uploaded birthday report .xlsx files. PINs come from uploaded PIN report PDF files.
Jobs come from the employee bio. Roles and departments are configured per location inside cfasuite-hr by the admin and assigned manually.

Authentication:
Send an API token with either header:
Authorization: Bearer <token>
X-API-Token: <token>

Tokens:
Create tokens in the admin UI at %s/tokens or with:
cfasuite-hr token create -name "Reporting"

Important data rules:
- Store/location numbers are strings. Preserve leading zeroes such as "03394".
- Employee numbers are strings.
- Employees are active employees from the latest employee bio import.
- job is the imported Chick-fil-A job field. role_id, role_name, department_id, and department_name are internal cfasuite-hr assignments and may be null.
- wage_rate_cents and wage_pay_type are learned from uploaded time punch reports. wage_pay_type is "hourly", "salary", or empty when unknown.
- exclude_from_labor is location-specific and means the employee is omitted from that location's Labor calculations.
- birth_date is ISO format YYYY-MM-DD when a birthday report matched the employee, and null when no birthday is known.
- Birthday reports are uploaded for one location and match employees at that location by exact Employee Name.
- clock_in_pin is a string from the location PIN report, or null when no PIN has been imported for that employee.
- PIN reports are uploaded for one location and match employees at that location by normalized Employee Name, allowing omitted middle names or initials.

Endpoints:
GET /api/v1/locations
Full URL: %s/api/v1/locations
Returns all Chick-fil-A locations with id, name, number, employee_count, created_at, and updated_at.

Example response:
{
  "locations": [
    {
      "id": 1,
      "name": "Southroads",
      "number": "03394",
      "employee_count": 42,
      "created_at": "2026-06-13T12:00:00Z",
      "updated_at": "2026-06-13T12:00:00Z"
    }
  ]
}

GET /api/v1/locations/{storeNumber}/employees
Full URL: %s/api/v1/locations/{storeNumber}/employees
Returns active employees for a location. Store numbers are strings, so leading zeroes matter.

Example response:
{
  "location": {"id": 1, "name": "Southroads", "number": "03394"},
  "employees": [
    {
      "id": 10,
      "location_id": 1,
      "employee_name": "Blanco, John",
      "employee_number": "12-1083836",
      "job": "Team Member",
      "role_id": 2,
      "role_name": "Trainer",
      "department_id": 1,
      "department_name": "Front of House",
      "employee_status": "Active",
      "location_latest_start_date": "2024-10-01",
      "birth_date": "1999-03-14",
      "clock_in_pin": "99129",
      "created_at": "2026-06-13T12:00:00Z",
      "updated_at": "2026-06-13T12:00:00Z"
    }
  ]
}

GET /api/v1/locations/{storeNumber}/employees/{employeeNumber}
Full URL: %s/api/v1/locations/{storeNumber}/employees/{employeeNumber}
Returns one employee by employee number.

Errors:
- 401 {"error":"valid API token required"} when the token is missing or invalid.
- 404 {"error":"location not found"} when the store number does not exist.
- 404 {"error":"employee not found"} when the employee number does not exist at that store.

cURL examples:
curl -sS -H "Authorization: Bearer $CFASUITE_TOKEN" "%s/api/v1/locations"
curl -sS -H "Authorization: Bearer $CFASUITE_TOKEN" "%s/api/v1/locations/03394/employees"
curl -sS -H "Authorization: Bearer $CFASUITE_TOKEN" "%s/api/v1/locations/03394/employees/12-1083836"

Go example:

package cfasuitehr

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type Employee struct {
	ID                      int64  `+"`json:\"id\"`"+`
	LocationID              int64  `+"`json:\"location_id\"`"+`
	EmployeeName            string `+"`json:\"employee_name\"`"+`
	EmployeeNumber          string `+"`json:\"employee_number\"`"+`
	Job                     string `+"`json:\"job\"`"+`
	RoleID                  *int64 `+"`json:\"role_id\"`"+`
	RoleName                *string `+"`json:\"role_name\"`"+`
	DepartmentID            *int64 `+"`json:\"department_id\"`"+`
	DepartmentName          *string `+"`json:\"department_name\"`"+`
	WageRateCents           *int64 `+"`json:\"wage_rate_cents\"`"+`
	WagePayType             string `+"`json:\"wage_pay_type\"`"+`
	ExcludeFromLabor        bool `+"`json:\"exclude_from_labor\"`"+`
	EmployeeStatus          string `+"`json:\"employee_status\"`"+`
	LocationLatestStartDate string `+"`json:\"location_latest_start_date\"`"+`
	BirthDate               *string `+"`json:\"birth_date\"`"+`
	ClockInPIN              *string `+"`json:\"clock_in_pin\"`"+`
}

func Employees(baseURL, token, storeNumber string) ([]Employee, error) {
	req, err := http.NewRequest("GET", baseURL+"/api/v1/locations/"+storeNumber+"/employees", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cfasuite-hr returned %%s", res.Status)
	}
	var payload struct {
		Employees []Employee `+"`json:\"employees\"`"+`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Employees, nil
}
`, strings.TrimRight(baseURL, "/"), strings.TrimRight(baseURL, "/"), strings.TrimRight(baseURL, "/"), strings.TrimRight(baseURL, "/"), strings.TrimRight(baseURL, "/"), strings.TrimRight(baseURL, "/"), strings.TrimRight(baseURL, "/"), strings.TrimRight(baseURL, "/"))
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

func pathID(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(r.PathValue(name), 10, 64)
}

func parseInt64Values(values []string) ([]int64, error) {
	ids := make([]int64, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func calendarMonthFromRequest(r *http.Request) (time.Time, error) {
	value := strings.TrimSpace(r.URL.Query().Get("month"))
	now := time.Now()
	if value == "" {
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local), nil
	}
	month, err := time.ParseInLocation("2006-01", value, time.Local)
	if err != nil {
		return time.Time{}, errors.New("month must use YYYY-MM format")
	}
	return time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.Local), nil
}

func calendarDays(month, today time.Time, salesDates, laborDates map[string]bool) []CalendarDay {
	first := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, month.Location())
	start := first.AddDate(0, 0, -int(first.Weekday()))
	todayDate := startOfDay(today)
	days := make([]CalendarDay, 0, 42)
	for i := 0; i < 42; i++ {
		current := start.AddDate(0, 0, i)
		currentDate := time.Date(current.Year(), current.Month(), current.Day(), 0, 0, 0, 0, current.Location())
		date := current.Format("2006-01-02")
		currentMonth := current.Month() == first.Month()
		sunday := current.Weekday() == time.Sunday
		accessible := currentDate.Before(todayDate)
		required := currentMonth && accessible && !sunday
		hasSales := salesDates[date]
		hasLabor := laborDates[date]
		days = append(days, CalendarDay{
			Date:          date,
			Label:         current.Format("Monday, January 2, 2006"),
			Day:           current.Day(),
			CurrentMonth:  currentMonth,
			Today:         currentDate.Equal(todayDate),
			HasSales:      hasSales,
			HasLabor:      hasLabor,
			SalesRequired: required,
			LaborRequired: required,
			Complete:      currentMonth && accessible && (sunday || (hasSales && hasLabor)),
			Sunday:        sunday,
			Accessible:    accessible,
		})
	}
	return days
}

func startOfDay(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, value.Location())
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func sign(secret []byte, payload string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func subtleEqual(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}

func clientIP(r *http.Request) string {
	for _, header := range []string{"X-Forwarded-For", "X-Real-IP"} {
		value := r.Header.Get(header)
		if value != "" {
			return strings.TrimSpace(strings.Split(value, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
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

var (
	punchLineRe                = regexp.MustCompile(`(?i)^\s*(Mon|Tue|Wed|Thu|Fri|Sat|Sun),\s*(\d{2}/\d{2}/\d{4})\s*\d{1,2}:\d{2}\s*[ap]\*?\s*(?:\d{1,2}:\d{2}\s*[ap]\*?|Open Punch)\s*(\d{1,4}:\d{2})\s*(Regular|Overtime|Unpaid|Break\s+\(Conv\s+To\s+Paid\))(.*)$`)
	employeeTotalsRe           = regexp.MustCompile(`^Employee Totals\s*(\d{1,4}:\d{2})(.*)$`)
	grandTotalsRe              = regexp.MustCompile(`^All Employees Grand Total\s*(\d{1,4}:\d{2})(.*)$`)
	laborTotalsDetailRe        = regexp.MustCompile(`^\s*(\d{1,4}:\d{2})\s+(\$[\d,]+\.\d{2})\s+(\d{1,4}:\d{2})\s+(\$[\d,]+\.\d{2})\s+(\$[\d,]+\.\d{2})`)
	durationRe                 = regexp.MustCompile(`\d{1,4}:\d{2}`)
	periodRe                   = regexp.MustCompile(`^From\s+([A-Za-z]+,\s+[A-Za-z]+\s+\d{1,2},\s+\d{4})\s+through\s+([A-Za-z]+,\s+[A-Za-z]+\s+\d{1,2},\s+\d{4})$`)
	moneyRe                    = regexp.MustCompile(`\$[\d,]+\.\d{2}`)
	employeeNameRe             = regexp.MustCompile(`^[A-Za-z][A-Za-z ,.'()/-]+$`)
	footerTimestampRe          = regexp.MustCompile(`^\d{2}/\d{2}/\d{4}\s+\d{2}:\d{2}:\d{2}\s+[AP]M`)
	reportHeaderRe             = regexp.MustCompile(`EmployeeNameDateTimeInTimeOutTotalTimePayTypeWageRateRegularOvertimeTotal WagesHoursWagesHoursWages`)
	fromPeriodInlineRe         = regexp.MustCompile(`([A-Za-z0-9.)])\s*From\s+([A-Za-z]+,\s+[A-Za-z]+\s+\d{1,2},\s+\d{4}\s+through\s+[A-Za-z]+,\s+[A-Za-z]+\s+\d{1,2},\s+\d{4})`)
	periodThenEmployeeRe       = regexp.MustCompile(`(From\s+[A-Za-z]+,\s+[A-Za-z]+\s+\d{1,2},\s+\d{4}\s+through\s+[A-Za-z]+,\s+[A-Za-z]+\s+\d{1,2},\s+\d{4})\s+([A-Z][A-Za-z .'\-/()]+,\s)`)
	employeeTotalsInlineRe     = regexp.MustCompile(`\s*Employee Totals\s*`)
	grandTotalsInlineRe        = regexp.MustCompile(`\s*All Employees Grand Total\s*`)
	moneyThenEmployeeRe        = regexp.MustCompile(`(\$[\d,]+\.\d{2})\s*([A-Z][A-Za-z .'\-/()]+,\s)`)
	weekdayInlineRe            = regexp.MustCompile(`\s+(Sun|Mon|Tue|Wed|Thu|Fri|Sat),\s*`)
	parentheticalNameRe        = regexp.MustCompile(`\(([^)]*)\)`)
	salesPeriodRe              = regexp.MustCompile(`^From\s+(.+)\s+through\s+(.+)$`)
	salesStoreRe               = regexp.MustCompile(`^Store:\s*(.+)$`)
	salesDaypartRe             = regexp.MustCompile(`^\d+\s*-\s*(Breakfast|Lunch|Afternoon|Dinner)\b`)
	salesMoneyRe               = regexp.MustCompile(`\d[\d,]*\.\d{2}`)
	salesGroupedMoneyOverlapRe = regexp.MustCompile(`(\d{1,3}(?:,\d{3})+\.\d{2})`)
	salesOnlyNumberRe          = regexp.MustCompile(`^[\d,.%]+$`)
)

var salesDayparts = []string{"Breakfast", "Lunch", "Afternoon", "Dinner"}

var salesDestinations = []string{
	"CARRY OUT",
	"DELIVERY",
	"DINE IN",
	"DRIVE THRU",
	"M-CARRYOUT",
	"M-DINEIN",
	"M-DRIVE-THRU",
	"ON DEMAND",
	"PICKUP",
}

var salesNoisePrefixes = []string{
	"Store:",
	"Report Time:",
	"Page:",
	"Daypart Activity Report",
	"Daypart/Destination",
	"Transaction",
	"Count",
	"Check",
	"Avg",
	"Labor",
	"Prod.*",
	"Labor %*",
	"Cumulative Totals",
	"*Daypart Activity Report",
	"Shifts Missing End Punch",
	"Employee Punch Start",
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func abs(path string) string {
	out, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return out
}

func parseTime(value string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t
		}
	}
	return time.Time{}
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

const layoutHTML = `{{define "layout"}}<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}} | cfasuite-hr</title>
  <link rel="stylesheet" href="/assets/app.css">
</head>
<body>
  <header>
    <a class="brand" href="/">cfasuite-hr</a>
    {{if not .LoggedOut}}<nav>
      <a href="/">Locations</a>
      <a href="/tokens">API Tokens</a>
      <a href="/docs">API Docs</a>
      <form method="post" action="/logout"><button class="ghost">Sign out</button></form>
    </nav>{{end}}
  </header>
  <main>{{template "body" .}}</main>
</body>
</html>{{end}}`

const loginHTML = `{{define "body"}}
<section class="narrow">
  <h1>Admin Login</h1>
  {{if .Error}}<p class="notice bad">{{.Error}}</p>{{end}}
  {{if not .Configured}}<p class="notice bad">Admin credentials are not configured. Set CFASUITE_ADMIN_USERNAME and CFASUITE_ADMIN_PASSWORD or run the set-admin CLI command.</p>{{end}}
  <form method="post" action="/login" class="panel">
    <label>Username <input name="username" autocomplete="username" required></label>
    <label>Password <input name="password" type="password" autocomplete="current-password" required></label>
    <button>Log in</button>
  </form>
</section>
{{end}}`

const dashboardHTML = `{{define "body"}}
<div class="row">
  <h1>Locations</h1>
  <a class="button" href="/locations/new">New location</a>
</div>
<section class="grid">
{{range .Locations}}
  <article class="card">
    <h2>{{.Name}}</h2>
    <p class="muted">Store {{.Number}}</p>
    <p>{{.Employees}} active employees</p>
    <a class="button secondary" href="/locations/{{.ID}}">Manage</a>
  </article>
{{else}}
  <p class="empty">No locations yet.</p>
{{end}}
</section>
{{end}}`

const locationFormHTML = `{{define "body"}}
<section class="narrow">
  <h1>{{.Mode}} Location</h1>
  <form method="post" action="{{.Action}}" class="panel">
    <label>Name <input name="name" value="{{.Location.Name}}" required></label>
    <label>Store number <input name="number" value="{{.Location.Number}}" required></label>
    <button>{{.Mode}}</button>
  </form>
</section>
{{end}}`

const locationShowHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>{{.Location.Name}}</h1>
    <p class="muted">Store {{.Location.Number}}</p>
  </div>
</div>
<nav class="portal-menu">
  <a class="active" href="/locations/{{.Location.ID}}">Overview</a>
  <a href="/locations/{{.Location.ID}}/details">Employee Details</a>
  <a href="/locations/{{.Location.ID}}/pay">Employee Pay</a>
  <a href="/locations/{{.Location.ID}}/calendar">Calendar</a>
  <a href="/locations/{{.Location.ID}}/sales">Sales</a>
  <a href="/locations/{{.Location.ID}}/documents">Documents</a>
  <a href="/locations/{{.Location.ID}}/edit">Edit</a>
  <a href="/locations/{{.Location.ID}}/labor">Labor</a>
  <a href="/locations/{{.Location.ID}}/departments">Departments</a>
  <a href="/locations/{{.Location.ID}}/roles">Roles</a>
</nav>
<section class="overview-grid">
  <article class="metric">
    <span>Store number</span>
    <strong>{{.Location.Number}}</strong>
  </article>
  <article class="metric">
    <span>Active employees</span>
    <strong>{{len .Employees}}</strong>
  </article>
  <article class="metric">
    <span>Created</span>
    <strong>{{.Location.CreatedAt.Format "2006-01-02"}}</strong>
  </article>
  <article class="metric">
    <span>Updated</span>
    <strong>{{.Location.UpdatedAt.Format "2006-01-02"}}</strong>
  </article>
</section>
<section>
  <div class="section-head compact">
    <h2>Employee overview</h2>
    <div class="assignment-status" aria-label="Assignment status">
      <span><strong id="role-unassigned-count">{{.AssignmentStatus.RoleUnassigned}}</strong> no role</span>
      <span><strong id="department-unassigned-count">{{.AssignmentStatus.DepartmentUnassigned}}</strong> no department</span>
    </div>
  </div>
  {{if .Import.Get "roles_assigned"}}<p class="notice">Updated roles for {{.Import.Get "roles_assigned"}} employees.</p>{{end}}
  {{if .Import.Get "departments_assigned"}}<p class="notice">Updated departments for {{.Import.Get "departments_assigned"}} employees.</p>{{end}}
  <div class="employee-filters">
    <label>Job
      <select id="employee-job-filter">
        <option value="">All jobs</option>
        {{range .JobOptions}}<option value="{{.}}">{{.}}</option>{{end}}
      </select>
    </label>
    <label>Role
      <select id="employee-role-filter">
        <option value="">All roles</option>
        <option value="__assigned">Assigned role</option>
        <option value="__unassigned">Unassigned role</option>
        {{range .Roles}}<option value="{{.ID}}">{{.Name}}</option>{{end}}
      </select>
    </label>
    <label>Department
      <select id="employee-department-filter">
        <option value="">All departments</option>
        <option value="__assigned">Assigned department</option>
        <option value="__unassigned">Unassigned department</option>
        {{range .Departments}}<option value="{{.ID}}">{{.Name}}</option>{{end}}
      </select>
    </label>
  </div>
  <table>
    <thead><tr><th>Name</th><th>Employee #</th><th>Job</th><th>Role</th><th>Department</th></tr></thead>
    <tbody id="employee-rows">
    {{range .Employees}}{{$employee := .}}<tr data-job="{{.Job}}" data-role="{{if .RoleID}}{{.RoleID}}{{else}}__unassigned{{end}}" data-department="{{if .DepartmentID}}{{.DepartmentID}}{{else}}__unassigned{{end}}"><td>{{.EmployeeName}}</td><td>{{.EmployeeNumber}}</td><td>{{.Job}}</td><td><form method="post" action="/locations/{{$.Location.ID}}/assignments" class="assignment-form"><input type="hidden" name="assignment" value="role"><input type="hidden" name="employee_id" value="{{.ID}}"><select name="role_id" aria-label="Role for {{.EmployeeName}}"><option value="" {{if not .RoleID}}selected{{end}}>Unassigned</option>{{range $.Roles}}<option value="{{.ID}}" {{if selectedID $employee.RoleID .ID}}selected{{end}}>{{.Name}}</option>{{end}}</select></form></td><td><form method="post" action="/locations/{{$.Location.ID}}/assignments" class="assignment-form"><input type="hidden" name="assignment" value="department"><input type="hidden" name="employee_id" value="{{.ID}}"><select name="department_id" aria-label="Department for {{.EmployeeName}}"><option value="" {{if not .DepartmentID}}selected{{end}}>Unassigned</option>{{range $.Departments}}<option value="{{.ID}}" {{if selectedID $employee.DepartmentID .ID}}selected{{end}}>{{.Name}}</option>{{end}}</select></form></td></tr>{{else}}<tr><td colspan="5">No employees imported.</td></tr>{{end}}
    <tr id="employee-filter-empty" hidden><td colspan="5">No employees match these filters.</td></tr>
    </tbody>
  </table>
</section>
<script>
(() => {
  const rows = Array.from(document.querySelectorAll('#employee-rows tr[data-job]'));
  const empty = document.getElementById('employee-filter-empty');
  const job = document.getElementById('employee-job-filter');
  const role = document.getElementById('employee-role-filter');
  const department = document.getElementById('employee-department-filter');
  const assignmentForms = Array.from(document.querySelectorAll('.assignment-form'));
  const roleUnassigned = document.getElementById('role-unassigned-count');
  const departmentUnassigned = document.getElementById('department-unassigned-count');
  function matchesAssignment(value, current) {
    if (!value) return true;
    if (value === '__assigned') return current !== '__unassigned';
    return current === value;
  }
  function visibleRows() {
    return rows.filter(row => !row.hidden);
  }
  function applyFilters() {
    rows.forEach(row => {
      const hidden = Boolean(job.value && row.dataset.job !== job.value) ||
        !matchesAssignment(role.value, row.dataset.role) ||
        !matchesAssignment(department.value, row.dataset.department);
      row.hidden = hidden;
    });
    if (empty) empty.hidden = visibleRows().length !== 0;
  }
  function updateAssignmentStatus() {
    if (roleUnassigned) roleUnassigned.textContent = rows.filter(row => row.dataset.role === '__unassigned').length;
    if (departmentUnassigned) departmentUnassigned.textContent = rows.filter(row => row.dataset.department === '__unassigned').length;
  }
  [job, role, department].forEach(control => control.addEventListener('input', applyFilters));
  assignmentForms.forEach(form => {
    const control = form.querySelector('select, input[type="checkbox"]');
    if (!control) return;
    control.dataset.previousValue = control.type === 'checkbox' ? String(control.checked) : control.value;
    control.addEventListener('change', async () => {
      const row = form.closest('tr');
      const assignment = form.querySelector('input[name="assignment"]').value;
      const previousValue = control.dataset.previousValue || '';
      const data = new FormData(form);
      control.disabled = true;
      try {
        const response = await fetch(form.action, {
          method: 'POST',
          body: data,
          headers: {'X-Requested-With': 'fetch'},
        });
        if (!response.ok) throw new Error(await response.text());
        control.dataset.previousValue = control.type === 'checkbox' ? String(control.checked) : control.value;
        if (assignment === 'role' || assignment === 'department') {
          row.dataset[assignment] = control.value || '__unassigned';
          updateAssignmentStatus();
          applyFilters();
        }
      } catch (error) {
        if (control.type === 'checkbox') {
          control.checked = previousValue === 'true';
        } else {
          control.value = previousValue;
        }
        alert((error.message || 'Unable to update assignment').trim());
      } finally {
        control.disabled = false;
      }
    });
  });
  updateAssignmentStatus();
  applyFilters();
})();
</script>
{{end}}`

const locationDetailsHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>{{.Location.Name}} Employee Details</h1>
    <p class="muted">Store {{.Location.Number}}</p>
  </div>
</div>
<nav class="portal-menu">
  <a href="/locations/{{.Location.ID}}">Overview</a>
  <a class="active" href="/locations/{{.Location.ID}}/details">Employee Details</a>
  <a href="/locations/{{.Location.ID}}/pay">Employee Pay</a>
  <a href="/locations/{{.Location.ID}}/calendar">Calendar</a>
  <a href="/locations/{{.Location.ID}}/sales">Sales</a>
  <a href="/locations/{{.Location.ID}}/documents">Documents</a>
  <a href="/locations/{{.Location.ID}}/edit">Edit</a>
  <a href="/locations/{{.Location.ID}}/labor">Labor</a>
  <a href="/locations/{{.Location.ID}}/departments">Departments</a>
  <a href="/locations/{{.Location.ID}}/roles">Roles</a>
</nav>
<section class="overview-grid">
  <article class="metric">
    <span>Store number</span>
    <strong>{{.Location.Number}}</strong>
  </article>
  <article class="metric">
    <span>Active employees</span>
    <strong>{{len .Employees}}</strong>
  </article>
  <article class="metric">
    <span>Created</span>
    <strong>{{.Location.CreatedAt.Format "2006-01-02"}}</strong>
  </article>
  <article class="metric">
    <span>Updated</span>
    <strong>{{.Location.UpdatedAt.Format "2006-01-02"}}</strong>
  </article>
</section>
<section>
  <div class="section-head">
    <h2>Employee details</h2>
    <label class="search-control">Name
      <input id="employee-detail-search" type="search" placeholder="Filter by name">
    </label>
  </div>
  <table>
    <thead><tr><th>Name</th><th>Start date</th><th>Birthday</th><th>Clock-in PIN</th></tr></thead>
    <tbody id="employee-detail-rows">
    {{range .Employees}}<tr data-name="{{.EmployeeName}}"><td>{{.EmployeeName}}</td><td>{{.LocationLatestStartDate}}</td><td>{{if .BirthDate}}{{.BirthDate}}{{else}}<span class="muted">Unknown</span>{{end}}</td><td>{{if .ClockInPIN}}{{.ClockInPIN}}{{else}}<span class="muted">Not imported</span>{{end}}</td></tr>{{else}}<tr><td colspan="4">No employees imported.</td></tr>{{end}}
    <tr id="employee-detail-empty" hidden><td colspan="4">No employees match this filter.</td></tr>
    </tbody>
  </table>
</section>
<script>
(() => {
  const rows = Array.from(document.querySelectorAll('#employee-detail-rows tr[data-name]'));
  const empty = document.getElementById('employee-detail-empty');
  const search = document.getElementById('employee-detail-search');
  if (search) {
    search.addEventListener('input', () => {
      const value = search.value.trim().toLowerCase();
      rows.forEach(row => row.hidden = value && !(row.dataset.name || '').toLowerCase().includes(value));
      if (empty) empty.hidden = rows.some(row => !row.hidden);
    });
  }
})();
</script>
{{end}}`

const locationPayHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>{{.Location.Name}} Employee Pay</h1>
    <p class="muted">Store {{.Location.Number}}</p>
  </div>
</div>
<nav class="portal-menu">
  <a href="/locations/{{.Location.ID}}">Overview</a>
  <a href="/locations/{{.Location.ID}}/details">Employee Details</a>
  <a class="active" href="/locations/{{.Location.ID}}/pay">Employee Pay</a>
  <a href="/locations/{{.Location.ID}}/calendar">Calendar</a>
  <a href="/locations/{{.Location.ID}}/sales">Sales</a>
  <a href="/locations/{{.Location.ID}}/documents">Documents</a>
  <a href="/locations/{{.Location.ID}}/edit">Edit</a>
  <a href="/locations/{{.Location.ID}}/labor">Labor</a>
  <a href="/locations/{{.Location.ID}}/departments">Departments</a>
  <a href="/locations/{{.Location.ID}}/roles">Roles</a>
</nav>
{{if .Import.Get "wages_assigned"}}<p class="notice">Updated wages for {{.Import.Get "wages_assigned"}} employee records.</p>{{end}}
{{if .Import.Get "labor_exclusions"}}<p class="notice">Updated labor exclusion for {{.Import.Get "labor_exclusions"}} employees.</p>{{end}}
<section>
  <div class="section-head">
    <h2>Employee pay</h2>
    <label class="search-control">Name
      <input id="employee-pay-search" type="search" placeholder="Filter by name">
    </label>
  </div>
  <table>
    <thead><tr><th>Name</th><th>Pay type</th><th>Wage</th><th>Exclude labor</th></tr></thead>
    <tbody id="employee-pay-rows">
    {{range .Employees}}<tr data-name="{{.EmployeeName}}"><td>{{.EmployeeName}}</td><td><form method="post" action="/locations/{{$.Location.ID}}/assignments" class="wage-form"><input type="hidden" name="assignment" value="wage"><input type="hidden" name="employee_id" value="{{.ID}}"><input type="hidden" name="wage_rate" value="{{formatWageInput .WageRateCents}}"><select name="wage_pay_type" aria-label="Pay type for {{.EmployeeName}}"><option value="" {{if eq .WagePayType ""}}selected{{end}}>Unknown</option><option value="hourly" {{if eq .WagePayType "hourly"}}selected{{end}}>Hourly</option><option value="salary" {{if eq .WagePayType "salary"}}selected{{end}}>Salary</option></select></form></td><td><form method="post" action="/locations/{{$.Location.ID}}/assignments" class="wage-form"><input type="hidden" name="assignment" value="wage"><input type="hidden" name="employee_id" value="{{.ID}}"><input type="hidden" name="wage_pay_type" value="{{.WagePayType}}"><input name="wage_rate" inputmode="decimal" value="{{formatWageInput .WageRateCents}}" placeholder="0.00" aria-label="Wage for {{.EmployeeName}}"></form></td><td><form method="post" action="/locations/{{$.Location.ID}}/assignments" class="assignment-form labor-exclusion-form"><input type="hidden" name="assignment" value="labor_exclusion"><input type="hidden" name="employee_id" value="{{.ID}}"><input type="hidden" name="exclude_from_labor" value="0"><input type="checkbox" name="exclude_from_labor" value="1" aria-label="Exclude {{.EmployeeName}} from labor calculations" {{if .ExcludeFromLabor}}checked{{end}}></form></td></tr>{{else}}<tr><td colspan="4">No employees imported.</td></tr>{{end}}
    <tr id="employee-pay-empty" hidden><td colspan="4">No employees match this filter.</td></tr>
    </tbody>
  </table>
</section>
<script>
(() => {
  const rows = Array.from(document.querySelectorAll('#employee-pay-rows tr[data-name]'));
  const empty = document.getElementById('employee-pay-empty');
  const search = document.getElementById('employee-pay-search');
  if (search) {
    search.addEventListener('input', () => {
      const value = search.value.trim().toLowerCase();
      rows.forEach(row => row.hidden = value && !(row.dataset.name || '').toLowerCase().includes(value));
      if (empty) empty.hidden = rows.some(row => !row.hidden);
    });
  }
  async function submitForm(form) {
    const controls = Array.from(form.querySelectorAll('input, select'));
    const previous = controls.map(control => control.type === 'checkbox' ? control.checked : control.value);
    const data = new FormData(form);
    controls.forEach(control => control.disabled = true);
    try {
      const response = await fetch(form.action, {
        method: 'POST',
        body: data,
        headers: {'X-Requested-With': 'fetch'},
      });
      if (!response.ok) throw new Error(await response.text());
    } catch (error) {
      controls.forEach((control, index) => {
        if (control.type === 'checkbox') control.checked = previous[index];
        else control.value = previous[index];
      });
      alert((error.message || 'Unable to update employee details').trim());
    } finally {
      controls.forEach(control => control.disabled = false);
    }
  }
  document.querySelectorAll('.labor-exclusion-form input[type="checkbox"]').forEach(control => {
    control.addEventListener('change', () => submitForm(control.form));
  });
  document.querySelectorAll('.wage-form select').forEach(control => {
    control.addEventListener('change', () => {
      const row = control.closest('tr');
      const wageInput = row ? row.querySelector('input[name="wage_rate"]:not([type="hidden"])') : null;
      const ownHiddenWage = control.form.querySelector('input[name="wage_rate"]');
      if (wageInput && ownHiddenWage) ownHiddenWage.value = wageInput.value;
      if (wageInput) {
        const hiddenPayType = wageInput.form.querySelector('input[name="wage_pay_type"]');
        if (hiddenPayType) hiddenPayType.value = control.value;
      }
      submitForm(control.form);
    });
  });
  document.querySelectorAll('.wage-form input[name="wage_rate"]').forEach(control => {
    if (control.type === 'hidden') return;
    control.addEventListener('change', () => {
      const row = control.closest('tr');
      const selectFormHiddenWage = row ? row.querySelector('select[name="wage_pay_type"]')?.form.querySelector('input[name="wage_rate"]') : null;
      if (selectFormHiddenWage) selectFormHiddenWage.value = control.value;
      submitForm(control.form);
    });
    control.addEventListener('keydown', event => {
      if (event.key === 'Enter') {
        event.preventDefault();
        control.blur();
      }
    });
  });
})();
</script>
{{end}}`

const locationCalendarHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>{{.Location.Name}} Calendar</h1>
    <p class="muted">Store {{.Location.Number}}</p>
  </div>
</div>
<nav class="portal-menu">
  <a href="/locations/{{.Location.ID}}">Overview</a>
  <a href="/locations/{{.Location.ID}}/details">Employee Details</a>
  <a href="/locations/{{.Location.ID}}/pay">Employee Pay</a>
  <a class="active" href="/locations/{{.Location.ID}}/calendar">Calendar</a>
  <a href="/locations/{{.Location.ID}}/sales">Sales</a>
  <a href="/locations/{{.Location.ID}}/documents">Documents</a>
  <a href="/locations/{{.Location.ID}}/edit">Edit</a>
  <a href="/locations/{{.Location.ID}}/labor">Labor</a>
  <a href="/locations/{{.Location.ID}}/departments">Departments</a>
  <a href="/locations/{{.Location.ID}}/roles">Roles</a>
</nav>
<section class="panel">
  <div class="section-head compact">
    <div>
      <h2>Sales upload calendar</h2>
      <p class="muted">Select a day to upload or review that day's sales report.</p>
    </div>
    <a class="button secondary" href="/locations/{{.Location.ID}}/sales">Generate sales report</a>
  </div>
  <div class="calendar-head">
    <a class="button secondary" href="/locations/{{.Location.ID}}/calendar?month={{.PrevMonth}}">Previous</a>
    <h2>{{.MonthLabel}}</h2>
    <a class="button secondary" href="/locations/{{.Location.ID}}/calendar?month={{.NextMonth}}">Next</a>
  </div>
  <div class="calendar-grid calendar-weekdays" aria-hidden="true">
    <span>Sun</span><span>Mon</span><span>Tue</span><span>Wed</span><span>Thu</span><span>Fri</span><span>Sat</span>
  </div>
  <div class="calendar-grid">
    {{range .Days}}{{if .Accessible}}<a class="calendar-day {{if not .CurrentMonth}}outside{{end}} {{if .Sunday}}sunday{{end}} {{if .Today}}today{{end}} {{if .Complete}}complete{{else if .SalesRequired}}missing-sales{{end}}" href="/locations/{{$.Location.ID}}/calendar/{{.Date}}" aria-label="{{.Label}}"><span>{{.Day}}</span><small>{{if .CurrentMonth}}{{if .Sunday}}Sunday{{else if .Complete}}Complete{{else if and .HasSales (not .HasLabor)}}Needs labor{{else if and .HasLabor (not .HasSales)}}Needs sales{{else}}Needs sales and labor{{end}}{{end}}</small></a>{{else}}<span class="calendar-day locked {{if not .CurrentMonth}}outside{{end}} {{if .Today}}today{{end}}" aria-label="{{.Label}}"><span>{{.Day}}</span><small>{{if .CurrentMonth}}{{if .Today}}Today{{else}}Future{{end}}{{end}}</small></span>{{end}}{{end}}
  </div>
</section>
{{end}}`

const locationCalendarDayHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>{{.Location.Name}} Calendar</h1>
    <p class="muted">Store {{.Location.Number}}</p>
  </div>
  <div class="actions">
    <a class="button secondary" href="{{.PrevDayURL}}">Previous day</a>
    <a class="button" href="{{.BackToMonth}}">Back to calendar</a>
    {{if .NextDayURL}}<a class="button secondary" href="{{.NextDayURL}}">Next day</a>{{end}}
  </div>
</div>
<nav class="portal-menu">
  <a href="/locations/{{.Location.ID}}">Overview</a>
  <a href="/locations/{{.Location.ID}}/details">Employee Details</a>
  <a href="/locations/{{.Location.ID}}/pay">Employee Pay</a>
  <a class="active" href="/locations/{{.Location.ID}}/calendar?month={{.MonthValue}}">Calendar</a>
  <a href="/locations/{{.Location.ID}}/sales">Sales</a>
  <a href="/locations/{{.Location.ID}}/documents">Documents</a>
  <a href="/locations/{{.Location.ID}}/edit">Edit</a>
  <a href="/locations/{{.Location.ID}}/labor">Labor</a>
  <a href="/locations/{{.Location.ID}}/departments">Departments</a>
  <a href="/locations/{{.Location.ID}}/roles">Roles</a>
</nav>
<section class="panel">
  <div class="section-head compact">
    <div>
      <h2>{{.DateLabel}}</h2>
      <p class="muted">Sales data for this calendar day</p>
    </div>
    <a class="button secondary" href="{{.BackToMonth}}">Back to calendar</a>
  </div>
  {{if .Import.Get "sales_imported"}}<p class="notice">Daypart activity report imported.</p>{{end}}
  {{if .Sales.BusinessDate}}
    <p><strong>{{formatMoney .Sales.TotalCents}}</strong> total sales</p>
    <section class="split">
      <div>
        <h2>Dayparts</h2>
        <table><tbody>{{range salesRowsForLabels .Sales.Dayparts salesDayparts}}<tr><td>{{.Label}}</td><td>{{formatMoney .Cents}}</td><td>{{.Percent}}</td></tr>{{end}}</tbody></table>
      </div>
      <div>
        <h2>Destinations</h2>
        <table><tbody>{{range salesRowsForLabels .Sales.Destinations salesDestinations}}<tr><td>{{.Label}}</td><td>{{formatMoney .Cents}}</td><td>{{.Percent}}</td></tr>{{end}}</tbody></table>
      </div>
    </section>
  {{else}}
    <p class="notice bad">Sales data has not been uploaded for this date.</p>
  {{end}}
</section>
<section class="panel">
  <div class="section-head compact">
    <div>
      <h2>Labor data</h2>
      <p class="muted">Time punch labor associated with this calendar day</p>
    </div>
    <a class="button secondary" href="{{.BackToMonth}}">Back to calendar</a>
  </div>
  {{if .Import.Get "labor_imported"}}<p class="notice">Time punch labor imported.</p>{{end}}
  {{if .Labor.BusinessDate}}
    <section class="overview-grid">
      <article class="metric"><span>Hours</span><strong>{{formatHours .Labor.TotalMinutes}}</strong></article>
      <article class="metric"><span>Overtime</span><strong>{{formatHours .Labor.OvertimeMinutes}}</strong></article>
      <article class="metric"><span>Labor dollars</span><strong>{{formatMoney .Labor.TotalWagesCents}}</strong></article>
      <article class="metric"><span>Date</span><strong>{{formatISODate .Labor.BusinessDate}}</strong></article>
    </section>
  {{else}}
    <p class="notice bad">Labor data has not been uploaded for this date.</p>
  {{end}}
</section>
<section class="panel">
  <div class="section-head compact">
    <h2>Upload sales data</h2>
    <a class="button secondary" href="{{.BackToMonth}}">Back to calendar</a>
  </div>
  <form method="post" action="/locations/{{.Location.ID}}/calendar/{{.Date}}/sales" enctype="multipart/form-data" class="labor-upload">
    <label>Daypart activity PDF
      <input type="file" name="daypart_activity" accept=".pdf" required>
    </label>
    <button>Upload sales</button>
  </form>
</section>
<section class="panel">
  <div class="section-head compact">
    <h2>Upload labor data</h2>
    <a class="button secondary" href="{{.BackToMonth}}">Back to calendar</a>
  </div>
  <form method="post" action="/locations/{{.Location.ID}}/calendar/{{.Date}}/labor" enctype="multipart/form-data" class="labor-upload">
    <label>Time punch PDF
      <input type="file" name="time_punch" accept=".pdf" required>
    </label>
    <button>Upload labor</button>
  </form>
</section>
{{end}}`

const locationSalesHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>{{.Location.Name}} Sales</h1>
    <p class="muted">Store {{.Location.Number}}</p>
  </div>
</div>
<nav class="portal-menu">
  <a href="/locations/{{.Location.ID}}">Overview</a>
  <a href="/locations/{{.Location.ID}}/details">Employee Details</a>
  <a href="/locations/{{.Location.ID}}/pay">Employee Pay</a>
  <a href="/locations/{{.Location.ID}}/calendar">Calendar</a>
  <a class="active" href="/locations/{{.Location.ID}}/sales">Sales</a>
  <a href="/locations/{{.Location.ID}}/documents">Documents</a>
  <a href="/locations/{{.Location.ID}}/edit">Edit</a>
  <a href="/locations/{{.Location.ID}}/labor">Labor</a>
  <a href="/locations/{{.Location.ID}}/departments">Departments</a>
  <a href="/locations/{{.Location.ID}}/roles">Roles</a>
</nav>
<form method="get" action="/locations/{{.Location.ID}}/sales" class="panel inline">
  <label>Start <input type="date" name="start" value="{{.StartDate}}" required></label>
  <label>End <input type="date" name="end" value="{{.EndDate}}" required></label>
  <button>Generate sales report</button>
</form>
{{if .Complete}}
  <section class="overview-grid">
    <article class="metric"><span>Required days</span><strong>{{.SelectedDateCount}}</strong></article>
    <article class="metric"><span>Imported days</span><strong>{{len .DailyRows}}</strong></article>
    <article class="metric"><span>Total sales</span><strong>{{formatMoney (salesRowsTotal .DaypartRows)}}</strong></article>
    <article class="metric"><span>Range</span><strong>{{formatISODate .StartDate}}</strong><em>{{formatISODate .EndDate}}</em></article>
  </section>
  <section>
    <h2>Sales by day</h2>
    <table><thead><tr><th>Date</th><th>Day</th><th>Total</th><th>Breakfast</th><th>Lunch</th><th>Afternoon</th><th>Dinner</th></tr></thead><tbody>{{range .DailyRows}}<tr><td><a class="table-link" href="{{calendarDayPath $.Location.ID .Date}}">{{.DateLabel}}</a></td><td>{{.Weekday}}</td><td>{{formatMoney .TotalCents}}</td>{{range .Dayparts}}<td>{{formatMoney .Cents}}</td>{{end}}</tr>{{else}}<tr><td colspan="7">No sales found.</td></tr>{{end}}</tbody></table>
  </section>
  <section class="split">
    <div>
      <h2>Sales by daypart</h2>
      <table><tbody>{{range .DaypartRows}}<tr><td>{{.Label}}</td><td>{{formatMoney .Cents}}</td><td>{{.Percent}}</td></tr>{{end}}</tbody></table>
    </div>
    <div>
      <h2>Sales by destination</h2>
      <table><tbody>{{range .DestinationRows}}<tr><td>{{.Label}}</td><td>{{formatMoney .Cents}}</td><td>{{.Percent}}</td></tr>{{end}}</tbody></table>
    </div>
  </section>
  <section>
    <h2>Sales by day of week</h2>
    <table><tbody>{{range .DayOfWeekRows}}<tr><td>{{.Label}}</td><td>{{formatMoney .Cents}}</td><td>{{.Percent}}</td></tr>{{end}}</tbody></table>
  </section>
{{else}}
  <section class="notice bad">
    <strong>Sales data is incomplete for this range.</strong>
    <p>Missing required non-Sunday dates: {{range .MissingDates}}<a class="missing-date-link" href="{{calendarDayPath $.Location.ID .}}">{{formatISODate .}}</a> {{end}}</p>
  </section>
{{end}}
{{end}}`

const locationDocumentsHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>{{.Location.Name}} Documents</h1>
    <p class="muted">Store {{.Location.Number}}</p>
  </div>
</div>
<nav class="portal-menu">
  <a href="/locations/{{.Location.ID}}">Overview</a>
  <a href="/locations/{{.Location.ID}}/details">Employee Details</a>
  <a href="/locations/{{.Location.ID}}/pay">Employee Pay</a>
  <a href="/locations/{{.Location.ID}}/calendar">Calendar</a>
  <a href="/locations/{{.Location.ID}}/sales">Sales</a>
  <a class="active" href="/locations/{{.Location.ID}}/documents">Documents</a>
  <a href="/locations/{{.Location.ID}}/edit">Edit</a>
  <a href="/locations/{{.Location.ID}}/labor">Labor</a>
  <a href="/locations/{{.Location.ID}}/departments">Departments</a>
  <a href="/locations/{{.Location.ID}}/roles">Roles</a>
</nav>
{{if .Import.Get "added"}}<p class="notice">Employee bio imported for {{.Location.Name}}. Added {{.Import.Get "added"}}, updated {{.Import.Get "updated"}}, removed {{.Import.Get "removed"}}, skipped {{.Import.Get "skipped"}}.</p>{{end}}
{{if .Import.Get "birthday_updated"}}<p class="notice">Birthday report imported for {{.Location.Name}}. Updated {{.Import.Get "birthday_updated"}} employee records. Skipped {{.Import.Get "birthday_skipped"}} rows that did not match current employees at this location.</p>{{end}}
{{if .Import.Get "pin_updated"}}<p class="notice">PIN report imported for {{.Location.Name}}. Updated {{.Import.Get "pin_updated"}} employee records. Skipped {{.Import.Get "pin_skipped"}} rows that did not match current employees at this location.</p>{{end}}
{{if .Import.Get "wages_imported"}}<p class="notice">Time punch wages imported for {{.Location.Name}}.</p>{{end}}
<section class="split">
  <form method="post" action="/locations/{{.Location.ID}}/upload" enctype="multipart/form-data" class="panel">
    <h2>Upload employee bio</h2>
    <p class="muted">This syncs active employees for this location and removes terminated or missing employees.</p>
    <input type="file" name="bio" accept=".xlsx" required>
    <button>Upload .xlsx</button>
  </form>
  <form method="post" action="/locations/{{.Location.ID}}/birthdays/upload" enctype="multipart/form-data" class="panel">
    <h2>Upload birthday report</h2>
    <p class="muted">This applies Employee Birthday Reader rows only to matching employees at this location.</p>
    <input type="file" name="birthdays" accept=".xlsx" required>
    <button>Upload birthdays</button>
  </form>
  <form method="post" action="/locations/{{.Location.ID}}/pins/upload" enctype="multipart/form-data" class="panel">
    <h2>Upload PIN report</h2>
    <p class="muted">This applies clock-in PINs only to matching employees at this location.</p>
    <input type="file" name="pins" accept=".pdf" required>
    <button>Upload PINs</button>
  </form>
  <form method="post" action="/locations/{{.Location.ID}}/documents/time-punch-wages" enctype="multipart/form-data" class="panel">
    <h2>Upload time punch wages</h2>
    <p class="muted">This reads wage rates from the time punch report and updates employee pay details.</p>
    <input type="file" name="time_punch" accept=".pdf" required>
    <button>Import wages</button>
  </form>
</section>
{{end}}`

const locationEditHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>Edit {{.Location.Name}}</h1>
    <p class="muted">Store {{.Location.Number}}</p>
  </div>
</div>
<nav class="portal-menu">
  <a href="/locations/{{.Location.ID}}">Overview</a>
  <a href="/locations/{{.Location.ID}}/details">Employee Details</a>
  <a href="/locations/{{.Location.ID}}/pay">Employee Pay</a>
  <a href="/locations/{{.Location.ID}}/calendar">Calendar</a>
  <a href="/locations/{{.Location.ID}}/sales">Sales</a>
  <a href="/locations/{{.Location.ID}}/documents">Documents</a>
  <a class="active" href="/locations/{{.Location.ID}}/edit">Edit</a>
  <a href="/locations/{{.Location.ID}}/labor">Labor</a>
  <a href="/locations/{{.Location.ID}}/departments">Departments</a>
  <a href="/locations/{{.Location.ID}}/roles">Roles</a>
</nav>
{{if .Saved}}<p class="notice">Location saved.</p>{{end}}
<section class="split">
  <form method="post" action="/locations/{{.Location.ID}}" class="panel">
    <h2>Edit location</h2>
    <label>Name <input name="name" value="{{.Location.Name}}" required></label>
    <label>Store number <input name="number" value="{{.Location.Number}}" required></label>
    <button>Save</button>
  </form>
  <section class="panel danger-zone">
    <h2>Delete location</h2>
    <p class="muted">Deleting a location also deletes its employee records.</p>
    <form method="post" action="/locations/{{.Location.ID}}/delete" onsubmit="return confirm('Delete this location and its employees?')"><button class="danger">Delete location</button></form>
  </section>
</section>
{{end}}`

const laborHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>{{.SelectedLocation.Name}} Labor</h1>
    <p class="muted">Store {{.SelectedLocation.Number}}</p>
  </div>
</div>
<nav class="portal-menu">
  <a href="/locations/{{.SelectedLocation.ID}}">Overview</a>
  <a href="/locations/{{.SelectedLocation.ID}}/details">Employee Details</a>
  <a href="/locations/{{.SelectedLocation.ID}}/pay">Employee Pay</a>
  <a href="/locations/{{.SelectedLocation.ID}}/calendar">Calendar</a>
  <a href="/locations/{{.SelectedLocation.ID}}/sales">Sales</a>
  <a href="/locations/{{.SelectedLocation.ID}}/documents">Documents</a>
  <a href="/locations/{{.SelectedLocation.ID}}/edit">Edit</a>
  <a class="active" href="/locations/{{.SelectedLocation.ID}}/labor">Labor</a>
  <a href="/locations/{{.SelectedLocation.ID}}/departments">Departments</a>
  <a href="/locations/{{.SelectedLocation.ID}}/roles">Roles</a>
</nav>
<form method="get" action="/locations/{{.SelectedLocation.ID}}/labor" class="panel inline">
  <label>Start <input type="date" name="start" value="{{.StartDate}}" required></label>
  <label>End <input type="date" name="end" value="{{.EndDate}}" required></label>
  <button>Generate labor report</button>
</form>
{{if .Complete}}
  <section class="overview-grid">
    {{range .LaborSummary}}<article class="metric"><span>{{.Label}}</span><strong>{{.Hours}}</strong><em>{{.Dollars}}</em>{{if .Detail}}<em>{{.Detail}}</em>{{end}}</article>{{end}}
  </section>
{{else}}
  <section class="notice bad">
    <strong>Labor data is incomplete for this range.</strong>
    <p>Missing required non-Sunday dates: {{range .MissingDates}}<a class="missing-date-link" href="{{calendarDayPath $.SelectedLocation.ID .}}">{{formatISODate .}}</a> {{end}}</p>
  </section>
{{end}}
<form method="post" action="/locations/{{.SelectedLocation.ID}}/labor" enctype="multipart/form-data" class="panel labor-upload">
  <label>Analyze a time punch report
    <input type="file" name="time_punch" accept=".pdf" required>
  </label>
  <button>Analyze labor</button>
</form>
{{if .Report}}
<section class="report-head">
  <div>
    <h2>{{.SelectedLocation.Name}}</h2>
    <p class="muted">Store {{.SelectedLocation.Number}}{{if .Report.LocationName}} · Report location: {{.Report.LocationName}}{{end}}</p>
    {{if .Report.PeriodLabel}}<p class="muted">{{.Report.PeriodLabel}}</p>{{end}}
  </div>
  <div class="summary-grid">
    {{range .Summary}}<article class="metric"><span>{{.Label}}</span><strong>{{.Hours}}</strong><em>{{.Dollars}}</em>{{if .Detail}}<em>{{.Detail}}</em>{{end}}</article>{{end}}
  </div>
</section>
<section>
  <h2>Labor by day of week</h2>
  <table>
    <thead><tr><th>Day</th><th>Dates included</th><th>Hours</th><th>Labor dollars</th><th>Total labor</th></tr></thead>
    <tbody>{{range .DayRows}}<tr><td>{{.Day}}</td><td>{{.Date}}</td><td>{{.Hours}}</td><td>{{.Dollars}}</td><td>{{.Percent}}</td></tr>{{else}}<tr><td colspan="5">No day labor found.</td></tr>{{end}}</tbody>
  </table>
</section>
<section>
  <h2>Labor by role</h2>
  <table>
    <thead><tr><th>Role</th><th>Hours</th><th>Labor dollars</th><th>Total labor</th></tr></thead>
    <tbody>{{range .RoleRows}}<tr><td>{{.Role}}</td><td>{{.Hours}}</td><td>{{.Dollars}}</td><td>{{.Percent}}</td></tr>{{else}}<tr><td colspan="4">No role labor found.</td></tr>{{end}}</tbody>
  </table>
</section>
<section>
  <h2>Labor by department</h2>
  <table>
    <thead><tr><th>Department</th><th>Hours</th><th>Labor dollars</th><th>Total labor</th></tr></thead>
    <tbody>{{range .DepartmentRows}}<tr><td>{{.Department}}</td><td>{{.Hours}}</td><td>{{.Dollars}}</td><td>{{.Percent}}</td></tr>{{else}}<tr><td colspan="4">No department labor found.</td></tr>{{end}}</tbody>
  </table>
</section>
<section>
  <h2>Labor by job</h2>
  <table>
    <thead><tr><th>Job</th><th>Hours</th><th>Labor dollars</th><th>Total labor</th></tr></thead>
    <tbody>{{range .JobRows}}<tr><td>{{.Job}}</td><td>{{.Hours}}</td><td>{{.Dollars}}</td><td>{{.Percent}}</td></tr>{{else}}<tr><td colspan="4">No job labor found.</td></tr>{{end}}</tbody>
  </table>
</section>
<section id="employee-labor">
  <div class="section-head">
    <h2>Labor by employee</h2>
    <div class="labor-controls">
      <label>Name
        <input id="employee-search" type="search" placeholder="Filter by name">
      </label>
      <label>Job
        <select id="employee-job-filter">
          <option value="">All jobs</option>
          {{range .EmployeeJobs}}<option value="{{.}}">{{.}}</option>{{end}}
        </select>
      </label>
      <label>Sort
        <select id="employee-sort">
          <option value="hours_desc">Hours high to low</option>
          <option value="hours_asc">Hours low to high</option>
          <option value="name_asc">Name A-Z</option>
          <option value="name_desc">Name Z-A</option>
          <option value="job_asc">Job A-Z</option>
          <option value="dollars_desc">Labor dollars high to low</option>
        </select>
      </label>
    </div>
  </div>
  <table>
    <thead><tr><th>Employee</th><th>Role</th><th>Department</th><th>Job</th><th>Hours</th><th>Labor dollars</th></tr></thead>
    <tbody id="employee-labor-rows">{{range .EmployeeRows}}<tr data-name="{{.Name}}" data-job="{{.Job}}" data-minutes="{{.MinutesValue}}" data-cents="{{.CentsValue}}"><td>{{.Name}}</td><td>{{.Role}}</td><td>{{.Department}}</td><td>{{.Job}}</td><td>{{.Hours}}</td><td>{{.Dollars}}</td></tr>{{else}}<tr><td colspan="6">No employee labor found.</td></tr>{{end}}</tbody>
  </table>
</section>
<script>
(() => {
  const tbody = document.getElementById('employee-labor-rows');
  if (!tbody) return;
  const rows = Array.from(tbody.querySelectorAll('tr[data-name]'));
  const search = document.getElementById('employee-search');
  const jobFilter = document.getElementById('employee-job-filter');
  const sort = document.getElementById('employee-sort');
  const text = (row, key) => (row.dataset[key] || '').toLowerCase();
  const number = (row, key) => Number(row.dataset[key] || 0);
  function compareRows(a, b) {
    switch (sort.value) {
    case 'hours_asc':
      return number(a, 'minutes') - number(b, 'minutes') || text(a, 'name').localeCompare(text(b, 'name'));
    case 'name_asc':
      return text(a, 'name').localeCompare(text(b, 'name'));
    case 'name_desc':
      return text(b, 'name').localeCompare(text(a, 'name'));
    case 'job_asc':
      return text(a, 'job').localeCompare(text(b, 'job')) || text(a, 'name').localeCompare(text(b, 'name'));
    case 'dollars_desc':
      return number(b, 'cents') - number(a, 'cents') || text(a, 'name').localeCompare(text(b, 'name'));
    default:
      return number(b, 'minutes') - number(a, 'minutes') || text(a, 'name').localeCompare(text(b, 'name'));
    }
  }
  function applyEmployeeControls() {
    const query = (search.value || '').trim().toLowerCase();
    const job = jobFilter.value;
    rows.sort(compareRows).forEach(row => {
      row.hidden = Boolean(query && !text(row, 'name').includes(query)) || Boolean(job && row.dataset.job !== job);
      tbody.appendChild(row);
    });
  }
  [search, jobFilter, sort].forEach(control => control.addEventListener('input', applyEmployeeControls));
  applyEmployeeControls();
})();
</script>
{{end}}
{{end}}`

const rolesHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>{{.SelectedLocation.Name}} Roles</h1>
    <p class="muted">Store {{.SelectedLocation.Number}}</p>
  </div>
</div>
<nav class="portal-menu">
  <a href="/locations/{{.SelectedLocation.ID}}">Overview</a>
  <a href="/locations/{{.SelectedLocation.ID}}/details">Employee Details</a>
  <a href="/locations/{{.SelectedLocation.ID}}/pay">Employee Pay</a>
  <a href="/locations/{{.SelectedLocation.ID}}/calendar">Calendar</a>
  <a href="/locations/{{.SelectedLocation.ID}}/sales">Sales</a>
  <a href="/locations/{{.SelectedLocation.ID}}/documents">Documents</a>
  <a href="/locations/{{.SelectedLocation.ID}}/edit">Edit</a>
  <a href="/locations/{{.SelectedLocation.ID}}/labor">Labor</a>
  <a href="/locations/{{.SelectedLocation.ID}}/departments">Departments</a>
  <a class="active" href="/locations/{{.SelectedLocation.ID}}/roles">Roles</a>
</nav>
<form method="post" action="/locations/{{.SelectedLocation.ID}}/roles/manage" class="panel inline">
  <label>Role name <input name="name" placeholder="Team Leader" required></label>
  <button>Create role</button>
</form>
<table>
  <thead><tr><th>Name</th><th>Assigned employees</th><th></th></tr></thead>
  <tbody>
  {{range .Roles}}
    <tr>
      <td>
        <form method="post" action="/locations/{{$.SelectedLocation.ID}}/roles/{{.ID}}" class="inline table-form">
          <label class="sr-only">Role name</label>
          <input name="name" value="{{.Name}}" required>
          <button class="small secondary">Save</button>
        </form>
      </td>
      <td>{{.Employees}}</td>
      <td><form method="post" action="/locations/{{$.SelectedLocation.ID}}/roles/{{.ID}}/delete" onsubmit="return confirm('Delete this role and clear it from assigned employees?')"><button class="danger small">Delete</button></form></td>
    </tr>
  {{else}}
    <tr><td colspan="3">No roles created.</td></tr>
  {{end}}
  </tbody>
</table>
{{end}}`

const departmentsHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>{{.SelectedLocation.Name}} Departments</h1>
    <p class="muted">Store {{.SelectedLocation.Number}}</p>
  </div>
</div>
<nav class="portal-menu">
  <a href="/locations/{{.SelectedLocation.ID}}">Overview</a>
  <a href="/locations/{{.SelectedLocation.ID}}/details">Employee Details</a>
  <a href="/locations/{{.SelectedLocation.ID}}/pay">Employee Pay</a>
  <a href="/locations/{{.SelectedLocation.ID}}/calendar">Calendar</a>
  <a href="/locations/{{.SelectedLocation.ID}}/sales">Sales</a>
  <a href="/locations/{{.SelectedLocation.ID}}/documents">Documents</a>
  <a href="/locations/{{.SelectedLocation.ID}}/edit">Edit</a>
  <a href="/locations/{{.SelectedLocation.ID}}/labor">Labor</a>
  <a class="active" href="/locations/{{.SelectedLocation.ID}}/departments">Departments</a>
  <a href="/locations/{{.SelectedLocation.ID}}/roles">Roles</a>
</nav>
<form method="post" action="/locations/{{.SelectedLocation.ID}}/departments/manage" class="panel inline">
  <label>Department name <input name="name" placeholder="Front of House" required></label>
  <button>Create department</button>
</form>
<table>
  <thead><tr><th>Name</th><th>Assigned employees</th><th></th></tr></thead>
  <tbody>
  {{range .Departments}}
    <tr>
      <td>
        <form method="post" action="/locations/{{$.SelectedLocation.ID}}/departments/{{.ID}}" class="inline table-form">
          <label class="sr-only">Department name</label>
          <input name="name" value="{{.Name}}" required>
          <button class="small secondary">Save</button>
        </form>
      </td>
      <td>{{.Employees}}</td>
      <td><form method="post" action="/locations/{{$.SelectedLocation.ID}}/departments/{{.ID}}/delete" onsubmit="return confirm('Delete this department and clear it from assigned employees?')"><button class="danger small">Delete</button></form></td>
    </tr>
  {{else}}
    <tr><td colspan="3">No departments created.</td></tr>
  {{end}}
  </tbody>
</table>
{{end}}`

const tokensHTML = `{{define "body"}}
<div class="row"><h1>API Tokens</h1></div>
{{if .NewToken}}<section class="notice"><strong>{{.NewTokenName}}</strong><code>{{.NewToken}}</code><p>Copy this now. It will not be shown again.</p></section>{{end}}
<form method="post" action="/tokens" class="panel inline">
  <label>Name <input name="name" required></label>
  <button>Create token</button>
</form>
<table>
  <thead><tr><th>Name</th><th>Prefix</th><th>Created</th><th>Last used</th><th></th></tr></thead>
  <tbody>{{range .Tokens}}<tr><td>{{.Name}}</td><td>{{.Prefix}}</td><td>{{.CreatedAt}}</td><td>{{if .LastUsedAt}}{{.LastUsedAt}}{{end}}</td><td><form method="post" action="/tokens/{{.ID}}/delete"><button class="danger small">Delete</button></form></td></tr>{{else}}<tr><td colspan="5">No tokens created.</td></tr>{{end}}</tbody>
</table>
{{end}}`

const docsHTML = `{{define "body"}}
<h1>API Docs</h1>
<section class="panel">
  <h2>Authentication</h2>
  <p>Use <code>Authorization: Bearer &lt;token&gt;</code> or <code>X-API-Token: &lt;token&gt;</code>.</p>
</section>
<section class="panel">
  <h2>Endpoints</h2>
  <table>
    <thead><tr><th>Method</th><th>URL</th><th>Returns</th></tr></thead>
    <tbody>
      <tr><td>GET</td><td><code>{{.BaseURL}}/api/v1/locations</code></td><td>Locations with store numbers and employee counts.</td></tr>
      <tr><td>GET</td><td><code>{{.BaseURL}}/api/v1/locations/{storeNumber}/employees</code></td><td>Active employees for one store, including assigned role, department, <code>birth_date</code>, and imported PINs when known.</td></tr>
      <tr><td>GET</td><td><code>{{.BaseURL}}/api/v1/locations/{storeNumber}/employees/{employeeNumber}</code></td><td>One active employee by employee number.</td></tr>
    </tbody>
  </table>
</section>
<section class="panel">
  <h2>Employee assignments</h2>
  <p>Roles and departments are created per location by the admin in cfasuite-hr and assigned manually to employees. The imported <code>job</code> field remains separate from role and department assignments. Employees without an assignment return <code>null</code> for those fields.</p>
</section>
<section class="panel">
  <h2>Employee birthdays</h2>
  <p>Birthdays are imported from an Employee Birthday Reader .xlsx file with <code>Employee Name</code> and <code>Birth Date</code> columns. The API returns <code>birth_date</code> as <code>YYYY-MM-DD</code> when known and <code>null</code> when no birthday has been imported for that employee.</p>
</section>
<section class="panel">
  <h2>Employee PINs</h2>
  <p>Clock-in PINs are imported from a location PIN report PDF with employee name, access level, clock-in PIN, and sign-in PIN columns. The importer ignores sign-in PINs and matches current employees at that location using normalized employee names.</p>
</section>
<section>
  <h2>LLM context and Go example</h2>
  <pre>{{.Context}}</pre>
</section>
{{end}}`

const appCSS = `
:root{color-scheme:dark;--bg:#050505;--panel:#111;--line:#262626;--text:#f5f5f5;--muted:#a3a3a3;--accent:#e51636;--bad:#ff6363}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:16px/1.45 system-ui,-apple-system,Segoe UI,sans-serif}a{color:inherit}header{min-height:64px;border-bottom:1px solid var(--line);display:flex;align-items:center;justify-content:space-between;padding:0 28px;background:#090909;position:sticky;top:0}nav{display:flex;gap:16px;align-items:center}nav a,.brand{text-decoration:none}.brand{font-weight:800}main{max-width:1120px;margin:0 auto;padding:32px 24px 64px}h1{font-size:34px;margin:0 0 18px}h2{font-size:20px;margin:0 0 14px}.row{display:flex;align-items:center;justify-content:space-between;gap:16px;margin-bottom:24px}.actions{display:flex;align-items:center;gap:10px;flex-wrap:wrap}.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(250px,1fr));gap:16px}.card,.panel,.notice{background:var(--panel);border:1px solid var(--line);border-radius:8px;padding:18px}.split{display:grid;grid-template-columns:1fr 1fr;gap:16px;margin-bottom:28px}.overview-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px;margin-bottom:28px}.portal-menu{display:flex;gap:8px;align-items:center;flex-wrap:wrap;border-bottom:1px solid var(--line);margin:-8px 0 28px;padding-bottom:12px}.portal-menu a{color:var(--muted);border:1px solid var(--line);border-radius:6px;padding:8px 12px;text-decoration:none}.portal-menu a.active{background:#222;color:var(--text);border-color:#3a3a3a}.narrow{max-width:520px;margin:8vh auto}.muted{color:var(--muted)}.empty{color:var(--muted);border:1px dashed var(--line);padding:24px;border-radius:8px}.bad{border-color:var(--bad);color:#ffd0d0}.danger-zone{border-color:#4a1f1f}.sr-only{position:absolute;width:1px;height:1px;padding:0;margin:-1px;overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap;border:0}form{margin:0}label{display:block;color:var(--muted);margin-bottom:14px}input,select{width:100%;margin-top:6px;background:#050505;color:var(--text);border:1px solid var(--line);border-radius:6px;padding:11px 12px}input[type=checkbox]{width:18px;height:18px;margin:0;accent-color:var(--accent)}button,.button{display:inline-flex;align-items:center;justify-content:center;min-height:40px;background:var(--accent);color:white;border:0;border-radius:6px;padding:0 14px;text-decoration:none;font-weight:700;cursor:pointer}.secondary{background:#222}.ghost{background:transparent;border:1px solid var(--line);color:var(--muted)}.danger{background:#7f1d1d}.small{min-height:32px;padding:0 10px}.inline{display:flex;gap:14px;align-items:end;margin-bottom:22px}.inline label{flex:1;margin:0}.table-form{margin:0}.table-link{color:var(--text);font-weight:700;text-decoration:underline;text-decoration-color:#3a3a3a;text-underline-offset:3px}.table-link:hover{color:white;text-decoration-color:var(--accent)}.employee-filters{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px;align-items:end;margin:0 0 14px}.employee-filters label{margin:0}.bulk-actions{display:grid;grid-template-columns:minmax(180px,280px) auto auto;gap:12px;align-items:end;margin:0 0 14px}.bulk-actions label{margin:0}.assignment-form select{min-width:150px;margin:0;padding:8px 10px}.labor-upload{display:grid;grid-template-columns:1fr auto;gap:14px;align-items:end;margin-bottom:28px}.labor-upload label{margin:0}.report-head{display:grid;grid-template-columns:1fr 1.4fr;gap:18px;align-items:start;margin-bottom:28px}.summary-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px}.metric{background:var(--panel);border:1px solid var(--line);border-radius:8px;padding:16px}.metric span,.metric em{display:block;color:var(--muted);font-style:normal}.metric strong{display:block;font-size:28px;line-height:1.1;margin:8px 0}.section-head{display:flex;align-items:end;justify-content:space-between;gap:16px;margin-bottom:14px}.section-head.compact{align-items:center}.section-head h2{margin:0}.section-head p{margin:4px 0 0}.assignment-status{display:flex;gap:8px;align-items:center;flex-wrap:wrap;color:var(--muted);font-size:14px}.assignment-status span{border:1px solid var(--line);border-radius:6px;padding:5px 8px}.assignment-status strong{color:var(--text);font-size:15px}.labor-controls{display:grid;grid-template-columns:minmax(180px,1fr) minmax(160px,1fr) minmax(210px,1.2fr);gap:12px;align-items:end;flex:1;max-width:760px}.labor-controls label{margin:0}.calendar-head{display:grid;grid-template-columns:auto 1fr auto;gap:12px;align-items:center;margin:18px 0}.calendar-head h2{text-align:center;margin:0}.calendar-grid{display:grid;grid-template-columns:repeat(7,minmax(0,1fr));gap:8px}.calendar-weekdays{margin-bottom:8px;color:var(--muted);font-size:13px;font-weight:700;text-align:center}.calendar-day{display:flex;min-height:112px;border:1px solid var(--line);border-radius:6px;background:#050505;padding:10px;text-decoration:none;flex-direction:column;justify-content:space-between}.calendar-day span{display:inline-flex;align-items:center;justify-content:center;width:30px;height:30px;border-radius:999px;font-weight:800}.calendar-day small{color:var(--muted);font-size:12px;line-height:1.2}.calendar-day.outside{color:#666;background:#080808}.calendar-day.outside small{visibility:hidden}.calendar-day.complete{border-color:#166534;background:#07130a}.calendar-day.missing-sales{border-color:#7f1d1d;background:#170808}.calendar-day.today span{background:var(--accent);color:white}.calendar-day.sunday.complete small{color:#9fd3aa}.calendar-day.locked{cursor:not-allowed;color:#777;background:#0a0a0a}.calendar-day.locked small{color:#666}section+section{margin-top:28px}table{width:100%;border-collapse:collapse;background:var(--panel);border:1px solid var(--line);border-radius:8px;overflow:hidden}th,td{text-align:left;border-bottom:1px solid var(--line);padding:12px;vertical-align:top}th{color:var(--muted);font-weight:600}tr[hidden]{display:none}code,pre{background:#030303;border:1px solid var(--line);border-radius:6px}code{padding:2px 5px}pre{padding:16px;overflow:auto;white-space:pre-wrap}.notice code,.missing-date-link{display:inline-block;margin:6px 6px 0 0;padding:6px 8px;overflow:auto;background:#030303;border:1px solid var(--line);border-radius:6px;color:var(--text);font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,Liberation Mono,monospace;font-size:.9em;text-decoration:none}.missing-date-link:hover{border-color:var(--accent);color:white}
@media (max-width:760px){header{height:auto;align-items:flex-start;gap:12px;padding:14px;flex-direction:column}nav{flex-wrap:wrap}.row,.split,.overview-grid,.inline,.employee-filters,.bulk-actions,.labor-upload,.report-head,.summary-grid,.section-head,.labor-controls{display:block}.row>*{margin-bottom:12px}.overview-grid .metric,.employee-filters label,.bulk-actions label,.bulk-actions button,.bulk-actions .button,.labor-upload label,.summary-grid .metric,.labor-controls label{margin-bottom:12px}.calendar-head{grid-template-columns:1fr 1fr}.calendar-head h2{grid-column:1/-1;grid-row:1;text-align:left}.calendar-head .button{grid-row:2}.calendar-grid{gap:5px}.calendar-day{min-height:72px;padding:6px}.calendar-day span{width:24px;height:24px}.calendar-day small{font-size:10px}main{padding:24px 14px}table{font-size:14px}th,td{padding:9px}}
`

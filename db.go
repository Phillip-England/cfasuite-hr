package main

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	_ "modernc.org/sqlite"
)

func mustDB(path string) *sql.DB {
	db, err := openDB(path)
	must(err)
	must(migrate(db))
	return db
}
func configuredDBPath() string {
	if path := strings.TrimSpace(os.Getenv("CFASUITE_DB_PATH")); path != "" {
		return path
	}
	if dir := strings.TrimSpace(os.Getenv("CFASUITE_DATA_DIR")); dir != "" {
		return filepath.Join(dir, defaultDBFile)
	}
	return defaultDBPath
}

func appDataDir() string {
	if dir := strings.TrimSpace(os.Getenv("CFASUITE_DATA_DIR")); dir != "" {
		return dir
	}
	dir := defaultAppDataDir()
	_ = os.Setenv("CFASUITE_DATA_DIR", dir)
	return dir
}

func defaultAppDataDir() string {
	switch runtime.GOOS {
	case "darwin":
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, "Library", "Application Support", appName)
		}
	case "windows":
		if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
			return filepath.Join(local, appName)
		}
		if roaming := strings.TrimSpace(os.Getenv("APPDATA")); roaming != "" {
			return filepath.Join(roaming, appName)
		}
	default:
		if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
			return filepath.Join(xdg, appName)
		}
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, ".local", "share", appName)
		}
	}
	return "data"
}

func appTempDir() (string, error) {
	dir := filepath.Join(appDataDir(), "tmp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
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
			email TEXT NOT NULL DEFAULT '',
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
		`CREATE TABLE IF NOT EXISTS monthly_productivity_goals (
			location_id INTEGER NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
			month TEXT NOT NULL,
			goal_basis_points INTEGER NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY(location_id, month)
		)`,
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
	if err := ensureColumn(db, "locations", "email", "TEXT NOT NULL DEFAULT ''"); err != nil {
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

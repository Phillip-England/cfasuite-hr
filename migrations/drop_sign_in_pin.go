//go:build ignore

package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	_ "modernc.org/sqlite"
)

func main() {
	dbPath := flag.String("db", "data/cfasuite-hr.db", "SQLite database path")
	flag.Parse()

	absDB, err := filepath.Abs(*dbPath)
	must(err)

	db, err := sql.Open("sqlite", absDB+"?_pragma=foreign_keys(0)&_pragma=busy_timeout(5000)")
	must(err)
	defer db.Close()
	db.SetMaxOpenConns(1)

	changed, err := migrate(db)
	must(err)
	must(cleanupFiles())
	if changed {
		fmt.Printf("removed employees.sign_in_pin from: %s\n", absDB)
		return
	}
	fmt.Printf("employees.sign_in_pin was already absent; cleaned up migration files: %s\n", absDB)
}

func migrate(db *sql.DB) (bool, error) {
	hasSignInPIN, err := hasColumn(db, "employees", "sign_in_pin")
	if err != nil {
		return false, err
	}
	if !hasSignInPIN {
		return false, nil
	}
	required := []string{
		"id",
		"location_id",
		"employee_name",
		"employee_number",
		"job",
		"role_id",
		"department_id",
		"wage_rate_cents",
		"wage_pay_type",
		"exclude_from_labor",
		"employee_status",
		"location_latest_start_date",
		"birth_date",
		"clock_in_pin",
		"created_at",
		"updated_at",
	}
	for _, column := range required {
		ok, err := hasColumn(db, "employees", column)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, fmt.Errorf("employees.%s is missing; start the current app once so normal migrations run, then rerun this cleanup", column)
		}
	}

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return false, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	stmts := []string{
		`CREATE TABLE employees_new (
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
		`INSERT INTO employees_new (
			id,
			location_id,
			employee_name,
			employee_number,
			job,
			role_id,
			department_id,
			wage_rate_cents,
			wage_pay_type,
			exclude_from_labor,
			employee_status,
			location_latest_start_date,
			birth_date,
			clock_in_pin,
			created_at,
			updated_at
		)
		SELECT
			id,
			location_id,
			employee_name,
			employee_number,
			job,
			role_id,
			department_id,
			wage_rate_cents,
			wage_pay_type,
			exclude_from_labor,
			employee_status,
			location_latest_start_date,
			birth_date,
			clock_in_pin,
			created_at,
			updated_at
		FROM employees`,
		`DROP TABLE employees`,
		`ALTER TABLE employees_new RENAME TO employees`,
		`CREATE INDEX IF NOT EXISTS idx_employees_location ON employees(location_id)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	if err := foreignKeyCheck(ctx, db); err != nil {
		return false, err
	}
	return true, nil
}

func foreignKeyCheck(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var table string
		var rowID int64
		var parent string
		var fkID int
		if err := rows.Scan(&table, &rowID, &parent, &fkID); err != nil {
			return err
		}
		return fmt.Errorf("foreign key violation after migration: table=%s rowid=%d parent=%s fkid=%d", table, rowID, parent, fkID)
	}
	return rows.Err()
}

func hasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func cleanupFiles() error {
	_, scriptPath, _, ok := runtime.Caller(0)
	if !ok {
		return errors.New("could not locate migration script for cleanup")
	}
	root := filepath.Dir(filepath.Dir(scriptPath))
	if err := os.Remove(filepath.Join(root, "migration.md")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(scriptPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

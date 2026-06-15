package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	dbPath := flag.String("db", "data/cfasuite-hr.db", "SQLite database path")
	flag.Parse()

	absDB, err := filepath.Abs(*dbPath)
	must(err)
	must(backupDB(absDB))

	db, err := sql.Open("sqlite", absDB+"?_pragma=foreign_keys(0)&_pragma=busy_timeout(5000)")
	must(err)
	defer db.Close()
	db.SetMaxOpenConns(1)

	must(migrate(db))
	must(cleanupFiles())
	fmt.Printf("migrated roles and departments to location-specific tables: %s\n", absDB)
}

func backupDB(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()

	backupPath := fmt.Sprintf("%s.backup-%s", path, time.Now().Format("20060102-150405"))
	out, err := os.OpenFile(backupPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	fmt.Printf("created backup: %s\n", backupPath)
	return nil
}

func migrate(db *sql.DB) error {
	locationSpecific, err := hasColumn(db, "roles", "location_id")
	if err != nil {
		return err
	}
	if locationSpecific {
		return errors.New("roles already have location_id; migration has already been applied")
	}
	departmentSpecific, err := hasColumn(db, "departments", "location_id")
	if err != nil {
		return err
	}
	if departmentSpecific {
		return errors.New("departments already have location_id; migration has already been applied")
	}

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmts := []string{
		`CREATE TABLE roles_new (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			location_id INTEGER NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(location_id, name)
		)`,
		`CREATE TABLE departments_new (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			location_id INTEGER NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(location_id, name)
		)`,
		`INSERT INTO roles_new (location_id, name, created_at, updated_at)
			SELECT l.id, r.name, r.created_at, r.updated_at
			FROM locations l
			CROSS JOIN roles r
			ORDER BY l.id, r.id`,
		`CREATE TEMP TABLE role_id_map AS
			SELECT r.id AS old_id, rn.location_id, rn.id AS new_id
			FROM roles r
			JOIN roles_new rn ON rn.name = r.name`,
		`UPDATE employees
			SET role_id = (
				SELECT new_id
				FROM role_id_map
				WHERE old_id = employees.role_id AND location_id = employees.location_id
			)
			WHERE role_id IS NOT NULL`,
		`INSERT INTO departments_new (location_id, name, created_at, updated_at)
			SELECT l.id, d.name, d.created_at, d.updated_at
			FROM locations l
			CROSS JOIN departments d
			ORDER BY l.id, d.id`,
		`CREATE TEMP TABLE department_id_map AS
			SELECT d.id AS old_id, dn.location_id, dn.id AS new_id
			FROM departments d
			JOIN departments_new dn ON dn.name = d.name`,
		`UPDATE employees
			SET department_id = (
				SELECT new_id
				FROM department_id_map
				WHERE old_id = employees.department_id AND location_id = employees.location_id
			)
			WHERE department_id IS NOT NULL`,
		`DROP TABLE roles`,
		`ALTER TABLE roles_new RENAME TO roles`,
		`DROP TABLE departments`,
		`ALTER TABLE departments_new RENAME TO departments`,
		`DROP TABLE role_id_map`,
		`DROP TABLE department_id_map`,
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := foreignKeyCheck(ctx, db); err != nil {
		return err
	}
	return nil
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
	if err := os.Remove(filepath.Dir(scriptPath)); err != nil && !errors.Is(err, os.ErrNotExist) {
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

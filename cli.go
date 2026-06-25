package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", env("CFASUITE_ADDR", ":"+defaultPort), "HTTP listen address")
	dbPath := fs.String("db", configuredDBPath(), "SQLite database path")
	fs.Parse(args)
	db, err := openDB(*dbPath)
	must(err)
	defer db.Close()
	must(migrate(db))
	app, err := newApp(db)
	must(err)
	log.Printf("%s listening on %s", appName, *addr)
	log.Printf("database: %s", abs(*dbPath))
	log.Printf("data directory: %s", abs(app.dataDir))
	must(http.ListenAndServe(*addr, app.routes()))
}

func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	dbPath := fs.String("db", configuredDBPath(), "SQLite database path")
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
		dbPath := fs.String("db", configuredDBPath(), "SQLite database path")
		fs.Parse(args[1:])
		fmt.Println(abs(*dbPath))
	case "reset":
		fs := flag.NewFlagSet("db reset", flag.ExitOnError)
		dbPath := fs.String("db", configuredDBPath(), "SQLite database path")
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
	dbPath := fs.String("db", configuredDBPath(), "SQLite database path")
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
		dbPath := fs.String("db", configuredDBPath(), "SQLite database path")
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
		dbPath := fs.String("db", configuredDBPath(), "SQLite database path")
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
		dbPath := fs.String("db", configuredDBPath(), "SQLite database path")
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

func cmdAPIKeyEnv(args []string) {
	fs := flag.NewFlagSet("api-key-env", flag.ExitOnError)
	apiKey := fs.String("api-key", "", "cfasuite-hr API key")
	fs.Parse(args)
	if *apiKey == "" {
		must(errors.New("api-key-env requires -api-key"))
	}
	fmt.Printf("export CFASUITE_HR_API_KEY=%q\n", *apiKey)
}

func cmdSetAPIKey(args []string) {
	fs := flag.NewFlagSet("set-api-key", flag.ExitOnError)
	apiKey := fs.String("api-key", "", "cfasuite-hr API key")
	envFile := fs.String("env-file", defaultShellEnvFile(), "shell environment file to update")
	fs.Parse(args)
	if *apiKey == "" && fs.NArg() > 0 {
		*apiKey = fs.Arg(0)
	}
	if *apiKey == "" {
		must(errors.New("set-api-key requires -api-key or an API key argument"))
	}
	if strings.ContainsAny(*apiKey, "\x00\r\n") {
		must(errors.New("api key must be a single-line value"))
	}
	if *envFile == "" {
		must(errors.New("could not determine shell environment file; pass -env-file"))
	}
	path := expandHome(*envFile)
	line := "export CFASUITE_HR_API_KEY=" + shellQuote(*apiKey)
	must(upsertEnvLine(path, "CFASUITE_HR_API_KEY", line))
	fmt.Printf("saved CFASUITE_HR_API_KEY in %s\n", path)
	fmt.Printf("run this once in your current shell: source %s\n", shellQuote(path))
}

func defaultShellEnvFile() string {
	if path := os.Getenv("CFASUITE_HR_ENV_FILE"); path != "" {
		return path
	}
	shell := filepath.Base(os.Getenv("SHELL"))
	switch shell {
	case "zsh":
		return "~/.zshrc"
	case "bash":
		return "~/.bashrc"
	default:
		return "~/.profile"
	}
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			if path == "~" {
				return home
			}
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func upsertEnvLine(path, name, line string) error {
	var lines []string
	perm := os.FileMode(0600)
	data, err := os.ReadFile(path)
	if err == nil {
		if info, statErr := os.Stat(path); statErr == nil {
			perm = info.Mode().Perm()
		}
		content := strings.TrimRight(string(data), "\n")
		if content != "" {
			lines = strings.Split(content, "\n")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	replaced := false
	for i, existing := range lines {
		trimmed := strings.TrimSpace(existing)
		if strings.HasPrefix(trimmed, "export "+name+"=") || strings.HasPrefix(trimmed, name+"=") {
			lines[i] = line
			replaced = true
		}
	}
	if !replaced {
		lines = append(lines, line)
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), perm)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

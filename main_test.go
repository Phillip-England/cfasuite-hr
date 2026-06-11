package main

import (
	"bytes"
	"mime/multipart"
	"os"
	"testing"
)

func TestParseBio(t *testing.T) {
	data, err := os.ReadFile("bio.xlsx")
	if err != nil {
		t.Skip("bio.xlsx fixture is not present")
	}
	employees, err := parseBio(data)
	if err != nil {
		t.Fatalf("parseBio returned error: %v", err)
	}
	if len(employees) != 3 {
		t.Fatalf("expected 3 employee rows, got %d", len(employees))
	}
	if employees[0].Name != "Blanco, John" || employees[0].Number != "12-1083836" {
		t.Fatalf("unexpected first employee: %#v", employees[0])
	}
	if employees[2].Status != "Terminated" {
		t.Fatalf("expected fixture to include terminated employee, got %#v", employees[2])
	}
}

func TestImportBioSyncsEmployees(t *testing.T) {
	data, err := os.ReadFile("bio.xlsx")
	if err != nil {
		t.Skip("bio.xlsx fixture is not present")
	}
	db, err := openDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	locationID, err := createLocation(db, "Southroads", "03394")
	if err != nil {
		t.Fatalf("createLocation: %v", err)
	}
	result, err := importBio(db, locationID, multipartFile{Reader: bytes.NewReader(data)}, &multipart.FileHeader{Filename: "bio.xlsx"})
	if err != nil {
		t.Fatalf("importBio: %v", err)
	}
	if result.Added != 2 || result.Skipped != 1 {
		t.Fatalf("unexpected import result: %#v", result)
	}
	employees, err := listEmployees(db, locationID)
	if err != nil {
		t.Fatalf("listEmployees: %v", err)
	}
	if len(employees) != 2 {
		t.Fatalf("expected 2 active employees, got %d", len(employees))
	}
	if employees[0].EmployeeNumber == "12-497944" || employees[1].EmployeeNumber == "12-497944" {
		t.Fatalf("terminated employee was imported: %#v", employees)
	}
}

type multipartFile struct {
	*bytes.Reader
}

func (f multipartFile) Close() error { return nil }

var _ multipart.File = multipartFile{}

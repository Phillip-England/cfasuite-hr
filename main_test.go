package main

import (
	"bytes"
	"html/template"
	"mime/multipart"
	"net/url"
	"os"
	"testing"

	"github.com/xuri/excelize/v2"
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

func TestParseBirthdays(t *testing.T) {
	data := birthdayWorkbook(t, [][]string{
		{"Employee Name", "Birth Date"},
		{"Blanco, John", "3/14/1999"},
		{"Diaz, Maria", "2001-07-04"},
	})
	birthdays, err := parseBirthdays(data)
	if err != nil {
		t.Fatalf("parseBirthdays returned error: %v", err)
	}
	if len(birthdays) != 2 {
		t.Fatalf("expected 2 birthday rows, got %d", len(birthdays))
	}
	if birthdays[0].Name != "Blanco, John" || birthdays[0].BirthDate != "1999-03-14" {
		t.Fatalf("unexpected first birthday: %#v", birthdays[0])
	}
	if birthdays[1].BirthDate != "2001-07-04" {
		t.Fatalf("unexpected second birthday: %#v", birthdays[1])
	}
}

func TestImportBirthdaysUpdatesMatchingEmployeesForLocation(t *testing.T) {
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
	otherLocationID, err := createLocation(db, "Northroads", "01234")
	if err != nil {
		t.Fatalf("createLocation: %v", err)
	}
	_, err = db.Exec(`INSERT INTO employees (location_id, employee_name, employee_number, job, employee_status, location_latest_start_date)
		VALUES (?, ?, ?, ?, ?, ?)`, locationID, "Blanco, John", "12-1083836", "Team Member", "Active", "2024-10-01")
	if err != nil {
		t.Fatalf("insert employee: %v", err)
	}
	_, err = db.Exec(`INSERT INTO employees (location_id, employee_name, employee_number, job, employee_status, location_latest_start_date)
		VALUES (?, ?, ?, ?, ?, ?)`, otherLocationID, "Blanco, John", "99-1083836", "Team Member", "Active", "2024-10-01")
	if err != nil {
		t.Fatalf("insert other employee: %v", err)
	}
	data := birthdayWorkbook(t, [][]string{
		{"Employee Name", "Birth Date"},
		{"Blanco, John", "3/14/1999"},
		{"Missing, Person", "1/2/2000"},
	})
	result, err := importBirthdays(db, locationID, multipartFile{Reader: bytes.NewReader(data)}, &multipart.FileHeader{Filename: "birthdays.xlsx"})
	if err != nil {
		t.Fatalf("importBirthdays: %v", err)
	}
	if result.Updated != 1 || result.Skipped != 1 {
		t.Fatalf("unexpected import result: %#v", result)
	}
	employee, err := getEmployee(db, locationID, "12-1083836")
	if err != nil {
		t.Fatalf("getEmployee: %v", err)
	}
	if employee.BirthDate == nil || *employee.BirthDate != "1999-03-14" {
		t.Fatalf("birth date was not imported: %#v", employee)
	}
	otherEmployee, err := getEmployee(db, otherLocationID, "99-1083836")
	if err != nil {
		t.Fatalf("get other employee: %v", err)
	}
	if otherEmployee.BirthDate != nil {
		t.Fatalf("birthday import crossed location boundary: %#v", otherEmployee)
	}
}

func TestAdminTemplatesRender(t *testing.T) {
	templates := []struct {
		name string
		body string
		data map[string]any
	}{
		{
			name: "dashboard",
			body: dashboardHTML,
			data: map[string]any{
				"Title":     "Locations",
				"Locations": []Location{},
				"Import":    url.Values{"birthday_updated": []string{"1"}, "birthday_skipped": []string{"0"}},
			},
		},
		{
			name: "location",
			body: locationShowHTML,
			data: map[string]any{
				"Title":    "Location",
				"Location": Location{ID: 1, Name: "Southroads", Number: "03394"},
				"Employees": []Employee{{
					EmployeeName:            "Blanco, John",
					EmployeeNumber:          "12-1083836",
					Job:                     "Team Member",
					EmployeeStatus:          "Active",
					LocationLatestStartDate: "2024-10-01",
					BirthDate:               stringPtr("1999-03-14"),
				}},
				"Import": url.Values{},
			},
		},
		{
			name: "docs",
			body: docsHTML,
			data: map[string]any{
				"Title":   "API Docs",
				"BaseURL": "https://hr.example.com",
				"Context": apiContext("https://hr.example.com"),
			},
		},
	}
	for _, tt := range templates {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, err := template.New("layout").Parse(layoutHTML + tt.body)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			var buf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&buf, "layout", tt.data); err != nil {
				t.Fatalf("ExecuteTemplate: %v", err)
			}
			if buf.Len() == 0 {
				t.Fatal("template rendered empty output")
			}
		})
	}
}

type multipartFile struct {
	*bytes.Reader
}

func (f multipartFile) Close() error { return nil }

var _ multipart.File = multipartFile{}

func birthdayWorkbook(t *testing.T, rows [][]string) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	sheet := f.GetSheetName(0)
	for r, row := range rows {
		for c, value := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				t.Fatalf("CoordinatesToCellName: %v", err)
			}
			if err := f.SetCellValue(sheet, cell, value); err != nil {
				t.Fatalf("SetCellValue: %v", err)
			}
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return buf.Bytes()
}

func stringPtr(value string) *string {
	return &value
}

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

func TestEmployeeRolesAreAssignedSeparatelyFromJobs(t *testing.T) {
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
	roleID, err := createRole(db, "Trainer")
	if err != nil {
		t.Fatalf("createRole: %v", err)
	}
	_, err = db.Exec(`INSERT INTO employees (location_id, employee_name, employee_number, job, employee_status, location_latest_start_date)
		VALUES (?, ?, ?, ?, ?, ?)`, locationID, "Blanco, John", "12-1083836", "Team Member", "Active", "2024-10-01")
	if err != nil {
		t.Fatalf("insert employee: %v", err)
	}
	employees, err := listEmployees(db, locationID)
	if err != nil {
		t.Fatalf("listEmployees: %v", err)
	}
	if len(employees) != 1 || employees[0].RoleID != nil || employees[0].RoleName != nil {
		t.Fatalf("new employee should start without a role: %#v", employees)
	}
	updated, err := assignEmployeeRole(db, locationID, []int64{employees[0].ID}, &roleID)
	if err != nil {
		t.Fatalf("assignEmployeeRole: %v", err)
	}
	if updated != 1 {
		t.Fatalf("expected one role assignment, got %d", updated)
	}
	employee, err := getEmployee(db, locationID, "12-1083836")
	if err != nil {
		t.Fatalf("getEmployee: %v", err)
	}
	if employee.Job != "Team Member" || employee.RoleName == nil || *employee.RoleName != "Trainer" {
		t.Fatalf("role assignment changed job or did not load role: %#v", employee)
	}
}

func TestEmployeeDepartmentsAreAssignedSeparatelyFromJobs(t *testing.T) {
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
	departmentID, err := createDepartment(db, "Front of House")
	if err != nil {
		t.Fatalf("createDepartment: %v", err)
	}
	_, err = db.Exec(`INSERT INTO employees (location_id, employee_name, employee_number, job, employee_status, location_latest_start_date)
		VALUES (?, ?, ?, ?, ?, ?)`, locationID, "Blanco, John", "12-1083836", "Team Member", "Active", "2024-10-01")
	if err != nil {
		t.Fatalf("insert employee: %v", err)
	}
	employees, err := listEmployees(db, locationID)
	if err != nil {
		t.Fatalf("listEmployees: %v", err)
	}
	if len(employees) != 1 || employees[0].DepartmentID != nil || employees[0].DepartmentName != nil {
		t.Fatalf("new employee should start without a department: %#v", employees)
	}
	updated, err := assignEmployeeDepartment(db, locationID, []int64{employees[0].ID}, &departmentID)
	if err != nil {
		t.Fatalf("assignEmployeeDepartment: %v", err)
	}
	if updated != 1 {
		t.Fatalf("expected one department assignment, got %d", updated)
	}
	employee, err := getEmployee(db, locationID, "12-1083836")
	if err != nil {
		t.Fatalf("getEmployee: %v", err)
	}
	if employee.Job != "Team Member" || employee.DepartmentName == nil || *employee.DepartmentName != "Front of House" {
		t.Fatalf("department assignment changed job or did not load department: %#v", employee)
	}
}

func TestImportBioPreservesRolesForRemainingEmployees(t *testing.T) {
	data := birthdayWorkbook(t, [][]string{
		{"Employee Name", "Employee Number", "Job", "Employee Status", "Location Latest Start Date"},
		{"Blanco, John", "12-1083836", "Team Member", "Active", "2024-10-01"},
	})
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
	roleID, err := createRole(db, "Trainer")
	if err != nil {
		t.Fatalf("createRole: %v", err)
	}
	_, err = db.Exec(`INSERT INTO employees (location_id, employee_name, employee_number, job, role_id, employee_status, location_latest_start_date)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, locationID, "Blanco, John", "12-1083836", "Team Member", roleID, "Active", "2024-10-01")
	if err != nil {
		t.Fatalf("insert employee: %v", err)
	}
	result, err := importBio(db, locationID, multipartFile{Reader: bytes.NewReader(data)}, &multipart.FileHeader{Filename: "bio.xlsx"})
	if err != nil {
		t.Fatalf("importBio: %v", err)
	}
	if result.Updated != 1 {
		t.Fatalf("expected one employee update, got %#v", result)
	}
	employee, err := getEmployee(db, locationID, "12-1083836")
	if err != nil {
		t.Fatalf("getEmployee: %v", err)
	}
	if employee.RoleName == nil || *employee.RoleName != "Trainer" {
		t.Fatalf("role was not preserved across bio sync: %#v", employee)
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

func TestParseTimePunchTextBuildsLaborRollups(t *testing.T) {
	report, err := parseTimePunchText(`Employee Time Detail
13th & Utica FSU
From Monday, May 11, 2026 through Monday, May 18, 2026
Baker, Ramond Manley (Ray)
Mon, 05/11/2026 8:00a 1:00p 5:00 Regular $15.00 5:00 $75.00 $75.00
Mon, 05/11/2026 1:00p 1:30p 0:30 Unpaid
Tue, 05/12/2026 8:00a 2:30p 6:30 Regular $15.00 6:30 $97.50 $97.50
Mon, 05/18/2026 8:00a 10:00a 2:00 Regular $15.00 2:00 $30.00 $30.00
Employee Totals 13:30 13:30 $202.50 $202.50
Escobar, Angel
Fri, 05/15/2026 9:00a 3:00p 6:00 Regular $14.00 6:00 $84.00 $84.00
Employee Totals 6:00 6:00 $84.00 $84.00
All Employees Grand Total 19:30 19:30 $286.50 $286.50
`)
	if err != nil {
		t.Fatalf("parseTimePunchText returned error: %v", err)
	}
	if report.LocationName != "13th & Utica FSU" || report.StartDate != "2026-05-11" || report.EndDate != "2026-05-18" {
		t.Fatalf("unexpected metadata: %#v", report)
	}
	if len(report.Employees) != 2 {
		t.Fatalf("expected 2 employees, got %d", len(report.Employees))
	}
	if report.GrandTotals.Minutes != 1170 || report.GrandTotals.WagesCents != 28650 {
		t.Fatalf("unexpected grand totals: %#v", report.GrandTotals)
	}
	dayRows := laborDayRows(report)
	if len(dayRows) != 3 {
		t.Fatalf("expected 3 day rows, got %#v", dayRows)
	}
	if dayRows[0].Day != "Monday" || dayRows[0].Date != "2026-05-11, 2026-05-18" || dayRows[0].Hours != "7.00" || dayRows[0].Dollars != "$105.00" || dayRows[0].Percent != "36.6%" {
		t.Fatalf("unexpected Monday rollup: %#v", dayRows[0])
	}
}

func TestParseTimePunchPDFSample(t *testing.T) {
	data, err := os.ReadFile("tp.pdf")
	if err != nil {
		t.Skip("tp.pdf fixture is not present")
	}
	report, err := parseTimePunchPDF(multipartFile{Reader: bytes.NewReader(data)}, &multipart.FileHeader{Filename: "tp.pdf"})
	if err != nil {
		t.Fatalf("parseTimePunchPDF returned error: %v", err)
	}
	if report.LocationName != "Southroads Shopping Center FSU" {
		t.Fatalf("unexpected location name: %q", report.LocationName)
	}
	if report.StartDate != "2026-06-07" || report.EndDate != "2026-06-20" {
		t.Fatalf("unexpected report period: %q through %q", report.StartDate, report.EndDate)
	}
	if len(report.Employees) < 80 {
		t.Fatalf("expected sample report employees to parse, got %d", len(report.Employees))
	}
	if report.GrandTotals.Minutes != 146658 || report.GrandTotals.WagesCents != 3907311 {
		t.Fatalf("unexpected grand totals: %#v", report.GrandTotals)
	}
	dayRows := laborDayRows(report)
	if len(dayRows) != 7 {
		t.Fatalf("expected 7 weekday rows, got %#v", dayRows)
	}
	if dayRows[0].Day != "Sunday" || dayRows[0].Date != "2026-06-07, 2026-06-14" || dayRows[0].Hours == "0.00" || dayRows[0].Dollars == "$0.00" || dayRows[0].Percent == "0.0%" {
		t.Fatalf("unexpected first day row: %#v", dayRows[0])
	}
}

func TestLaborJobRowsUsesEmployeeJobs(t *testing.T) {
	report, err := parseTimePunchText(`Employee Time Detail
Store
From Monday, May 11, 2026 through Saturday, May 16, 2026
Baker, Ramond Manley (Ray)
Mon, 05/11/2026 8:00a 1:00p 5:00 Regular $15.00 5:00 $75.00 $75.00
Employee Totals 5:00 5:00 $75.00 $75.00
Escobar, Angel
Fri, 05/15/2026 9:00a 3:00p 6:00 Regular $14.00 6:00 $84.00 $84.00
Employee Totals 6:00 6:00 $84.00 $84.00
`)
	if err != nil {
		t.Fatalf("parseTimePunchText returned error: %v", err)
	}
	applyEmployeeJobs(&report, []Employee{
		{EmployeeName: "Baker, Ramond Manley (Ray)", Job: "Kitchen"},
		{EmployeeName: "Escobar, Angel", Job: "Front Counter"},
	})
	rows := laborJobRows(report)
	if len(rows) != 2 {
		t.Fatalf("expected 2 job rows, got %#v", rows)
	}
	if rows[0].Job != "Front Counter" || rows[0].Hours != "6.00" || rows[0].Dollars != "$84.00" || rows[0].Percent != "52.8%" {
		t.Fatalf("unexpected first job row: %#v", rows[0])
	}
}

func TestLaborRowsUseEmployeeRoleAndDepartmentAssignments(t *testing.T) {
	report, err := parseTimePunchText(`Employee Time Detail
Store
From Monday, May 11, 2026 through Saturday, May 16, 2026
Baker, Ramond Manley (Ray)
Mon, 05/11/2026 8:00a 1:00p 5:00 Regular $15.00 5:00 $75.00 $75.00
Employee Totals 5:00 5:00 $75.00 $75.00
Escobar, Angel
Fri, 05/15/2026 9:00a 3:00p 6:00 Regular $14.00 6:00 $84.00 $84.00
Employee Totals 6:00 6:00 $84.00 $84.00
`)
	if err != nil {
		t.Fatalf("parseTimePunchText returned error: %v", err)
	}
	trainer := "Trainer"
	foh := "Front of House"
	kitchen := "Kitchen"
	applyEmployeeAssignments(&report, []Employee{
		{EmployeeName: "Baker, Ramond Manley (Ray)", Job: "Kitchen", RoleName: &trainer, DepartmentName: &kitchen},
		{EmployeeName: "Escobar, Angel", Job: "Front Counter", RoleName: &trainer, DepartmentName: &foh},
	})
	roleRows := laborRoleRows(report)
	if len(roleRows) != 1 {
		t.Fatalf("expected 1 role row, got %#v", roleRows)
	}
	if roleRows[0].Role != "Trainer" || roleRows[0].Hours != "11.00" || roleRows[0].Dollars != "$159.00" || roleRows[0].Percent != "100.0%" {
		t.Fatalf("unexpected role row: %#v", roleRows[0])
	}
	departmentRows := laborDepartmentRows(report)
	if len(departmentRows) != 2 {
		t.Fatalf("expected 2 department rows, got %#v", departmentRows)
	}
	if departmentRows[0].Department != "Front of House" || departmentRows[0].Hours != "6.00" || departmentRows[0].Dollars != "$84.00" || departmentRows[0].Percent != "52.8%" {
		t.Fatalf("unexpected first department row: %#v", departmentRows[0])
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
				"Roles":    []Role{{ID: 1, Name: "Trainer"}},
				"Departments": []Department{
					{ID: 1, Name: "Front of House"},
				},
				"JobOptions": []string{"Team Member"},
				"Employees": []Employee{{
					EmployeeName:            "Blanco, John",
					EmployeeNumber:          "12-1083836",
					Job:                     "Team Member",
					RoleID:                  int64Ptr(1),
					RoleName:                stringPtr("Trainer"),
					DepartmentID:            int64Ptr(1),
					DepartmentName:          stringPtr("Front of House"),
					EmployeeStatus:          "Active",
					LocationLatestStartDate: "2024-10-01",
					BirthDate:               stringPtr("1999-03-14"),
				}},
				"Import": url.Values{},
			},
		},
		{
			name: "location documents",
			body: locationDocumentsHTML,
			data: map[string]any{
				"Title":    "Documents",
				"Location": Location{ID: 1, Name: "Southroads", Number: "03394"},
				"Import":   url.Values{"added": []string{"1"}, "updated": []string{"2"}, "removed": []string{"0"}, "skipped": []string{"0"}},
			},
		},
		{
			name: "location edit",
			body: locationEditHTML,
			data: map[string]any{
				"Title":    "Edit",
				"Location": Location{ID: 1, Name: "Southroads", Number: "03394"},
				"Saved":    "1",
			},
		},
		{
			name: "roles",
			body: rolesHTML,
			data: map[string]any{
				"Title": "Roles",
				"Roles": []Role{{ID: 1, Name: "Trainer", Employees: 2}},
			},
		},
		{
			name: "departments",
			body: departmentsHTML,
			data: map[string]any{
				"Title":       "Departments",
				"Departments": []Department{{ID: 1, Name: "Front of House", Employees: 2}},
			},
		},
		{
			name: "labor",
			body: laborHTML,
			data: map[string]any{
				"Title":            "Labor",
				"SelectedLocation": Location{ID: 1, Name: "Southroads", Number: "03394"},
				"Report":           TimePunchReport{PeriodLabel: "From Monday, May 11, 2026 through Saturday, May 16, 2026"},
				"Summary":          []LaborSummary{{Label: "Total week", Hours: "10.00", Dollars: "$100.00"}},
				"DayRows":          []LaborDayRow{{Day: "Monday", Date: "2026-05-11", Hours: "10.00", Dollars: "$100.00", Percent: "100.0%"}},
				"RoleRows":         []LaborEmployeeRow{{Role: "Trainer", Hours: "10.00", Dollars: "$100.00", Percent: "100.0%"}},
				"DepartmentRows":   []LaborEmployeeRow{{Department: "Front of House", Hours: "10.00", Dollars: "$100.00", Percent: "100.0%"}},
				"EmployeeRows":     []LaborEmployeeRow{{Name: "Blanco, John", Job: "Team Member", Role: "Trainer", Department: "Front of House", Hours: "10.00", Dollars: "$100.00"}},
				"EmployeeJobs":     []string{"Team Member"},
				"JobRows":          []LaborEmployeeRow{{Job: "Team Member", Hours: "10.00", Dollars: "$100.00", Percent: "100.0%"}},
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
			tmpl, err := template.New("layout").Funcs(templateFuncs()).Parse(layoutHTML + tt.body)
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

func int64Ptr(value int64) *int64 {
	return &value
}

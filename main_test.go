package main

import (
	"bytes"
	"encoding/base64"
	"html/template"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestMigrateAddsTimePunchTokenToExistingLocations(t *testing.T) {
	db, err := openDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE locations (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		number TEXT NOT NULL UNIQUE,
		email TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("create old locations table: %v", err)
	}
	_, err = db.Exec(`INSERT INTO locations (name, number, email) VALUES (?, ?, ?)`, "Southroads", "03394", "southroads@example.com")
	if err != nil {
		t.Fatalf("insert old location: %v", err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var token string
	if err := db.QueryRow(`SELECT time_punch_token FROM locations WHERE number = ?`, "03394").Scan(&token); err != nil {
		t.Fatalf("select time_punch_token: %v", err)
	}
	if token == "" {
		t.Fatal("expected migration to populate time_punch_token")
	}
	var indexCount int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_locations_time_punch_token'`).Scan(&indexCount); err != nil {
		t.Fatalf("select index: %v", err)
	}
	if indexCount != 1 {
		t.Fatalf("expected time punch token index to exist, got %d", indexCount)
	}
}

func TestEmployeeProfilePhotoColumnsAndUpdate(t *testing.T) {
	db, err := openDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	locationID, err := createLocation(db, "Southroads", "03394", "southroads@example.com")
	if err != nil {
		t.Fatalf("createLocation: %v", err)
	}
	res, err := db.Exec(`INSERT INTO employees (location_id, employee_name, employee_number, job, employee_status, location_latest_start_date)
		VALUES (?, ?, ?, ?, ?, ?)`, locationID, "Blanco, John", "12-1083836", "Team Member", "Active", "2024-10-01")
	if err != nil {
		t.Fatalf("insert employee: %v", err)
	}
	employeeID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	employee, err := getEmployeeByID(db, locationID, employeeID)
	if err != nil {
		t.Fatalf("getEmployeeByID: %v", err)
	}
	if !employeeNeedsProfilePhoto(employee) {
		t.Fatal("new employee should need a profile photo")
	}
	photo := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("photo"))
	if err := updateEmployeeProfilePhoto(db, locationID, employeeID, photo); err != nil {
		t.Fatalf("updateEmployeeProfilePhoto: %v", err)
	}
	employee, err = getEmployeeByID(db, locationID, employeeID)
	if err != nil {
		t.Fatalf("getEmployeeByID after photo update: %v", err)
	}
	if employee.ProfilePhotoDataURL != photo || employee.ProfilePhotoNeedsUpdate || employeeNeedsProfilePhoto(employee) {
		t.Fatalf("photo update did not persist clean state: %#v", employee)
	}
	if err := flagEmployeeProfilePhoto(db, locationID, employeeID); err != nil {
		t.Fatalf("flagEmployeeProfilePhoto: %v", err)
	}
	employee, err = getEmployeeByID(db, locationID, employeeID)
	if err != nil {
		t.Fatalf("getEmployeeByID after flag: %v", err)
	}
	if !employee.ProfilePhotoNeedsUpdate || !employeeNeedsProfilePhoto(employee) {
		t.Fatalf("flag did not require a new photo: %#v", employee)
	}
}

func TestSaveEmployeeProfilePhotoWritesToLocationDirectory(t *testing.T) {
	db, err := openDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	locationID, err := createLocation(db, "Southroads", "03394", "southroads@example.com")
	if err != nil {
		t.Fatalf("createLocation: %v", err)
	}
	res, err := db.Exec(`INSERT INTO employees (location_id, employee_name, employee_number, job, employee_status, location_latest_start_date)
		VALUES (?, ?, ?, ?, ?, ?)`, locationID, "Blanco, John", "12-1083836", "Team Member", "Active", "2024-10-01")
	if err != nil {
		t.Fatalf("insert employee: %v", err)
	}
	employeeID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	loc, err := getLocation(db, locationID)
	if err != nil {
		t.Fatalf("getLocation: %v", err)
	}
	employee, err := getEmployeeByID(db, locationID, employeeID)
	if err != nil {
		t.Fatalf("getEmployeeByID: %v", err)
	}
	dataDir := t.TempDir()
	photo := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("photo"))
	photoURL, err := saveEmployeeProfilePhoto(dataDir, loc, employee, photo)
	if err != nil {
		t.Fatalf("saveEmployeeProfilePhoto: %v", err)
	}
	if !strings.HasPrefix(photoURL, "/locations/"+strconv.FormatInt(locationID, 10)+"/employees/"+strconv.FormatInt(employeeID, 10)+"/photo/image?v=") {
		t.Fatalf("unexpected photo URL: %s", photoURL)
	}
	path := filepath.Join(dataDir, "locations", "03394", "profile-pictures", "employee-"+strconv.FormatInt(employeeID, 10)+".jpg")
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved photo: %v", err)
	}
	if string(written) != "photo" {
		t.Fatalf("saved photo contents = %q", string(written))
	}
	if err := updateEmployeeProfilePhoto(db, locationID, employeeID, photoURL); err != nil {
		t.Fatalf("updateEmployeeProfilePhoto: %v", err)
	}
	app := &App{db: db, dataDir: dataDir}
	req := httptest.NewRequest("GET", photoURL, nil)
	req.SetPathValue("locationID", strconv.FormatInt(locationID, 10))
	req.SetPathValue("id", strconv.FormatInt(employeeID, 10))
	rec := httptest.NewRecorder()
	app.employeePhotoImage(rec, req)
	if rec.Code != 200 || rec.Body.String() != "photo" {
		t.Fatalf("employeePhotoImage status = %d body = %q", rec.Code, rec.Body.String())
	}
}

func TestEmployeeProfileRenders(t *testing.T) {
	db, err := openDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	locationID, err := createLocation(db, "Southroads", "03394", "southroads@example.com")
	if err != nil {
		t.Fatalf("createLocation: %v", err)
	}
	res, err := db.Exec(`INSERT INTO employees (location_id, employee_name, employee_number, job, employee_status, location_latest_start_date)
		VALUES (?, ?, ?, ?, ?, ?)`, locationID, "Blanco, John", "12-1083836", "Team Member", "Active", "2024-10-01")
	if err != nil {
		t.Fatalf("insert employee: %v", err)
	}
	employeeID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	app, err := newApp(db)
	if err != nil {
		t.Fatalf("newApp: %v", err)
	}
	req := httptest.NewRequest("GET", "/employee", nil)
	req.SetPathValue("locationID", strconv.FormatInt(locationID, 10))
	req.SetPathValue("id", strconv.FormatInt(employeeID, 10))
	rec := httptest.NewRecorder()
	app.employeeProfile(rec, req)
	if rec.Code != 200 {
		t.Fatalf("employeeProfile status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Upload profile photo") || !strings.Contains(body, "Blanco, John") || !strings.Contains(body, "/forms/profile-photo/") {
		t.Fatalf("employee profile did not render expected content: %s", body)
	}
}

func TestPublicEmployeePhotoLinkUploadsProfilePhoto(t *testing.T) {
	db, err := openDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	locationID, err := createLocation(db, "Southroads", "03394", "southroads@example.com")
	if err != nil {
		t.Fatalf("createLocation: %v", err)
	}
	res, err := db.Exec(`INSERT INTO employees (location_id, employee_name, employee_number, job, employee_status, location_latest_start_date)
		VALUES (?, ?, ?, ?, ?, ?)`, locationID, "Blanco, John", "12-1083836", "Team Member", "Active", "2024-10-01")
	if err != nil {
		t.Fatalf("insert employee: %v", err)
	}
	employeeID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	loc, err := getLocation(db, locationID)
	if err != nil {
		t.Fatalf("getLocation: %v", err)
	}
	employee, err := getEmployeeByID(db, locationID, employeeID)
	if err != nil {
		t.Fatalf("getEmployeeByID: %v", err)
	}
	app := &App{db: db, dataDir: t.TempDir(), sessionSecret: []byte("test-secret")}
	token := app.employeePhotoToken(loc, employee)

	req := httptest.NewRequest("GET", "/forms/profile-photo/"+token, nil)
	req.SetPathValue("token", token)
	rec := httptest.NewRecorder()
	app.publicEmployeePhotoForm(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Profile photo") || !strings.Contains(rec.Body.String(), "Blanco, John") {
		t.Fatalf("public photo form did not render, status = %d body: %s", rec.Code, rec.Body.String())
	}

	var reqBody bytes.Buffer
	writer := multipart.NewWriter(&reqBody)
	photo := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("photo"))
	if err := writer.WriteField("profile_photo_data_url", photo); err != nil {
		t.Fatalf("write multipart field: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req = httptest.NewRequest("POST", "/forms/profile-photo/"+token, &reqBody)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.SetPathValue("token", token)
	rec = httptest.NewRecorder()
	app.publicEmployeePhotoSubmit(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Your profile photo was uploaded.") {
		t.Fatalf("public photo submit did not render success, status = %d body: %s", rec.Code, rec.Body.String())
	}
	employee, err = getEmployeeByID(db, locationID, employeeID)
	if err != nil {
		t.Fatalf("getEmployeeByID after upload: %v", err)
	}
	if employee.ProfilePhotoDataURL == "" || employee.ProfilePhotoNeedsUpdate {
		t.Fatalf("public upload did not update employee photo state: %#v", employee)
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
	locationID, err := createLocation(db, "Southroads", "03394", "southroads@example.com")
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
	locationID, err := createLocation(db, "Southroads", "03394", "southroads@example.com")
	if err != nil {
		t.Fatalf("createLocation: %v", err)
	}
	roleID, err := createRole(db, locationID, "Trainer")
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
	locationID, err := createLocation(db, "Southroads", "03394", "southroads@example.com")
	if err != nil {
		t.Fatalf("createLocation: %v", err)
	}
	departmentID, err := createDepartment(db, locationID, "Front of House")
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

func TestRoleAndDepartmentNamesCanRepeatAcrossLocations(t *testing.T) {
	db, err := openDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	firstLocationID, err := createLocation(db, "Southroads", "03394", "southroads@example.com")
	if err != nil {
		t.Fatalf("create first location: %v", err)
	}
	secondLocationID, err := createLocation(db, "Downtown", "01234", "downtown@example.com")
	if err != nil {
		t.Fatalf("create second location: %v", err)
	}
	if _, err := createRole(db, firstLocationID, "Trainer"); err != nil {
		t.Fatalf("create first role: %v", err)
	}
	if _, err := createRole(db, secondLocationID, "Trainer"); err != nil {
		t.Fatalf("create second role: %v", err)
	}
	if _, err := createDepartment(db, firstLocationID, "Front of House"); err != nil {
		t.Fatalf("create first department: %v", err)
	}
	if _, err := createDepartment(db, secondLocationID, "Front of House"); err != nil {
		t.Fatalf("create second department: %v", err)
	}
	roles, err := listRoles(db, firstLocationID)
	if err != nil {
		t.Fatalf("listRoles: %v", err)
	}
	if len(roles) != 1 || roles[0].LocationID != firstLocationID || roles[0].Name != "Trainer" {
		t.Fatalf("unexpected first location roles: %#v", roles)
	}
	departments, err := listDepartments(db, secondLocationID)
	if err != nil {
		t.Fatalf("listDepartments: %v", err)
	}
	if len(departments) != 1 || departments[0].LocationID != secondLocationID || departments[0].Name != "Front of House" {
		t.Fatalf("unexpected second location departments: %#v", departments)
	}
}

func TestEmployeeWagesAreAssignedByEmployeeNumber(t *testing.T) {
	db, err := openDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	firstLocationID, err := createLocation(db, "Southroads", "03394", "southroads@example.com")
	if err != nil {
		t.Fatalf("create first location: %v", err)
	}
	secondLocationID, err := createLocation(db, "Downtown", "01234", "downtown@example.com")
	if err != nil {
		t.Fatalf("create second location: %v", err)
	}
	for _, locationID := range []int64{firstLocationID, secondLocationID} {
		_, err = db.Exec(`INSERT INTO employees (location_id, employee_name, employee_number, job, employee_status, location_latest_start_date)
			VALUES (?, ?, ?, ?, ?, ?)`, locationID, "Manager, Sally", "99", "Director", "Active", "2024-10-01")
		if err != nil {
			t.Fatalf("insert employee: %v", err)
		}
	}
	employees, err := listEmployees(db, firstLocationID)
	if err != nil {
		t.Fatalf("listEmployees: %v", err)
	}
	wage := int64(750000)
	updated, err := assignEmployeeWage(db, firstLocationID, []int64{employees[0].ID}, &wage, "salary")
	if err != nil {
		t.Fatalf("assignEmployeeWage: %v", err)
	}
	if updated != 2 {
		t.Fatalf("expected wage assignment to update both location rows, got %d", updated)
	}
	for _, locationID := range []int64{firstLocationID, secondLocationID} {
		employee, err := getEmployee(db, locationID, "99")
		if err != nil {
			t.Fatalf("getEmployee: %v", err)
		}
		if employee.WageRateCents == nil || *employee.WageRateCents != 750000 || employee.WagePayType != "salary" {
			t.Fatalf("wage assignment did not propagate to location %d: %#v", locationID, employee)
		}
	}
}

func TestEmployeeLaborExclusionPersistsForLocation(t *testing.T) {
	db, err := openDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	locationID, err := createLocation(db, "Southroads", "03394", "southroads@example.com")
	if err != nil {
		t.Fatalf("createLocation: %v", err)
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
	if _, err := assignEmployeeLaborExclusion(db, locationID, []int64{employees[0].ID}, true); err != nil {
		t.Fatalf("assignEmployeeLaborExclusion: %v", err)
	}
	employees, err = listEmployees(db, locationID)
	if err != nil {
		t.Fatalf("listEmployees after exclusion: %v", err)
	}
	if len(employees) != 1 || !employees[0].ExcludeFromLabor {
		t.Fatalf("expected labor exclusion to persist, got %#v", employees)
	}
}

func TestEmployeeAssignmentsStayWithinLocation(t *testing.T) {
	db, err := openDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	firstLocationID, err := createLocation(db, "Southroads", "03394", "southroads@example.com")
	if err != nil {
		t.Fatalf("create first location: %v", err)
	}
	secondLocationID, err := createLocation(db, "Downtown", "01234", "downtown@example.com")
	if err != nil {
		t.Fatalf("create second location: %v", err)
	}
	roleID, err := createRole(db, firstLocationID, "Trainer")
	if err != nil {
		t.Fatalf("createRole: %v", err)
	}
	departmentID, err := createDepartment(db, firstLocationID, "Front of House")
	if err != nil {
		t.Fatalf("createDepartment: %v", err)
	}
	for _, locationID := range []int64{firstLocationID, secondLocationID} {
		_, err = db.Exec(`INSERT INTO employees (location_id, employee_name, employee_number, job, employee_status, location_latest_start_date)
			VALUES (?, ?, ?, ?, ?, ?)`, locationID, "Blanco, John", "12-1083836", "Team Member", "Active", "2024-10-01")
		if err != nil {
			t.Fatalf("insert employee: %v", err)
		}
	}
	employees, err := listEmployees(db, firstLocationID)
	if err != nil {
		t.Fatalf("listEmployees: %v", err)
	}
	updated, err := assignEmployeeRole(db, firstLocationID, []int64{employees[0].ID}, &roleID)
	if err != nil {
		t.Fatalf("assignEmployeeRole: %v", err)
	}
	if updated != 1 {
		t.Fatalf("expected role assignment to update one location row, got %d", updated)
	}
	updated, err = assignEmployeeDepartment(db, firstLocationID, []int64{employees[0].ID}, &departmentID)
	if err != nil {
		t.Fatalf("assignEmployeeDepartment: %v", err)
	}
	if updated != 1 {
		t.Fatalf("expected department assignment to update one location row, got %d", updated)
	}
	firstEmployee, err := getEmployee(db, firstLocationID, "12-1083836")
	if err != nil {
		t.Fatalf("get first employee: %v", err)
	}
	if firstEmployee.RoleName == nil || *firstEmployee.RoleName != "Trainer" || firstEmployee.DepartmentName == nil || *firstEmployee.DepartmentName != "Front of House" {
		t.Fatalf("assignment was not applied to first location: %#v", firstEmployee)
	}
	secondEmployee, err := getEmployee(db, secondLocationID, "12-1083836")
	if err != nil {
		t.Fatalf("get second employee: %v", err)
	}
	if secondEmployee.RoleName != nil || secondEmployee.DepartmentName != nil {
		t.Fatalf("assignment crossed location boundary: %#v", secondEmployee)
	}
}

func TestImportBioDoesNotInheritRoleOrDepartmentAcrossLocations(t *testing.T) {
	db, err := openDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	firstLocationID, err := createLocation(db, "Southroads", "03394", "southroads@example.com")
	if err != nil {
		t.Fatalf("create first location: %v", err)
	}
	secondLocationID, err := createLocation(db, "Downtown", "01234", "downtown@example.com")
	if err != nil {
		t.Fatalf("create second location: %v", err)
	}
	roleID, err := createRole(db, firstLocationID, "Trainer")
	if err != nil {
		t.Fatalf("createRole: %v", err)
	}
	departmentID, err := createDepartment(db, firstLocationID, "Front of House")
	if err != nil {
		t.Fatalf("createDepartment: %v", err)
	}
	_, err = db.Exec(`INSERT INTO employees (location_id, employee_name, employee_number, job, role_id, department_id, employee_status, location_latest_start_date)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, firstLocationID, "Blanco, John", "12-1083836", "Team Member", roleID, departmentID, "Active", "2024-10-01")
	if err != nil {
		t.Fatalf("insert employee: %v", err)
	}
	data := birthdayWorkbook(t, [][]string{
		{"Employee Name", "Employee Number", "Job", "Employee Status", "Location Latest Start Date"},
		{"Blanco, John", "12-1083836", "Team Member", "Active", "2024-10-01"},
	})
	result, err := importBio(db, secondLocationID, multipartFile{Reader: bytes.NewReader(data)}, &multipart.FileHeader{Filename: "bio.xlsx"})
	if err != nil {
		t.Fatalf("importBio: %v", err)
	}
	if result.Added != 1 {
		t.Fatalf("expected import to add employee, got %#v", result)
	}
	employee, err := getEmployee(db, secondLocationID, "12-1083836")
	if err != nil {
		t.Fatalf("getEmployee: %v", err)
	}
	if employee.RoleName != nil || employee.DepartmentName != nil {
		t.Fatalf("imported employee inherited assignment across locations: %#v", employee)
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
	locationID, err := createLocation(db, "Southroads", "03394", "southroads@example.com")
	if err != nil {
		t.Fatalf("createLocation: %v", err)
	}
	roleID, err := createRole(db, locationID, "Trainer")
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
	locationID, err := createLocation(db, "Southroads", "03394", "southroads@example.com")
	if err != nil {
		t.Fatalf("createLocation: %v", err)
	}
	otherLocationID, err := createLocation(db, "Northroads", "01234", "northroads@example.com")
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

func TestParsePinsPDF(t *testing.T) {
	data, err := os.ReadFile("pin.pdf")
	if err != nil {
		t.Skip("pin.pdf fixture is not present")
	}
	pins, err := parsePinsPDF(data)
	if err != nil {
		t.Fatalf("parsePinsPDF: %v", err)
	}
	if len(pins) < 80 {
		t.Fatalf("expected sample PIN report employees to parse, got %d", len(pins))
	}
	if pins[0].Name != "Aguirre, Angel" || pins[0].ClockInPIN != "99129" {
		t.Fatalf("unexpected first PIN row: %#v", pins[0])
	}
	var foundTeamMember bool
	for _, pin := range pins {
		if pin.Name == "Barbour, Sullivan" {
			foundTeamMember = true
			if pin.ClockInPIN != "721506" {
				t.Fatalf("unexpected team member PIN row: %#v", pin)
			}
		}
		if pin.Name == "Kyle Sutton" {
			t.Fatalf("employee without a clock-in PIN should be skipped: %#v", pin)
		}
	}
	if !foundTeamMember {
		t.Fatal("expected Barbour, Sullivan to parse")
	}
}

func TestImportPinsUpdatesMatchingEmployeesForLocation(t *testing.T) {
	db, err := openDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	locationID, err := createLocation(db, "Southroads", "03394", "southroads@example.com")
	if err != nil {
		t.Fatalf("createLocation: %v", err)
	}
	otherLocationID, err := createLocation(db, "Northroads", "01234", "northroads@example.com")
	if err != nil {
		t.Fatalf("createLocation: %v", err)
	}
	_, err = db.Exec(`INSERT INTO employees (location_id, employee_name, employee_number, job, employee_status, location_latest_start_date)
		VALUES (?, ?, ?, ?, ?, ?)`, locationID, "Aguirre, Angel", "1", "Team Member", "Active", "2024-10-01")
	if err != nil {
		t.Fatalf("insert employee: %v", err)
	}
	_, err = db.Exec(`INSERT INTO employees (location_id, employee_name, employee_number, job, employee_status, location_latest_start_date)
		VALUES (?, ?, ?, ?, ?, ?)`, otherLocationID, "Aguirre, Angel", "2", "Team Member", "Active", "2024-10-01")
	if err != nil {
		t.Fatalf("insert other employee: %v", err)
	}
	data, err := os.ReadFile("pin.pdf")
	if err != nil {
		t.Skip("pin.pdf fixture is not present")
	}
	result, err := importPins(db, locationID, multipartFile{Reader: bytes.NewReader(data)}, &multipart.FileHeader{Filename: "pins.pdf"})
	if err != nil {
		t.Fatalf("importPins: %v", err)
	}
	if result.Updated != 1 || result.Skipped == 0 {
		t.Fatalf("unexpected import result: %#v", result)
	}
	employee, err := getEmployee(db, locationID, "1")
	if err != nil {
		t.Fatalf("getEmployee: %v", err)
	}
	if employee.ClockInPIN == nil || *employee.ClockInPIN != "99129" {
		t.Fatalf("clock-in PIN was not imported: %#v", employee)
	}
	otherEmployee, err := getEmployee(db, otherLocationID, "2")
	if err != nil {
		t.Fatalf("get other employee: %v", err)
	}
	if otherEmployee.ClockInPIN != nil {
		t.Fatalf("PIN import crossed location boundary: %#v", otherEmployee)
	}
}

func TestParsePinsTextAcceptsUnknownEmployeeGroups(t *testing.T) {
	pins, err := parsePinsText(`Full Name
Employee Group
Clock-In PIN
Sign-In PIN
Vasquez, Rafael
Kitchen Lead
123456
123456`)
	if err != nil {
		t.Fatalf("parsePinsText: %v", err)
	}
	if len(pins) != 1 || pins[0].Name != "Vasquez, Rafael" || pins[0].ClockInPIN != "123456" {
		t.Fatalf("unexpected PIN rows: %#v", pins)
	}
}

func TestTimePunchCorrectionUsesClockInPINForLocation(t *testing.T) {
	db, err := openDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	locationID, err := createLocation(db, "Southroads", "03394", "southroads@example.com")
	if err != nil {
		t.Fatalf("createLocation: %v", err)
	}
	_, err = db.Exec(`INSERT INTO employees (location_id, employee_name, employee_number, job, employee_status, location_latest_start_date, clock_in_pin)
		VALUES (?, 'Blanco, John', '12-1083836', 'Team Member', 'Active', '2026-01-01', '99129')`, locationID)
	if err != nil {
		t.Fatalf("insert employee: %v", err)
	}
	employee, err := getEmployeeByClockInPIN(db, locationID, "99129")
	if err != nil {
		t.Fatalf("getEmployeeByClockInPIN: %v", err)
	}
	if err := createTimePunchCorrection(db, TimePunchCorrection{
		LocationID:   locationID,
		EmployeeID:   employee.ID,
		ClockInPIN:   "99129",
		BusinessDate: "2026-06-13",
		StartTime:    "08:00",
		EndTime:      "15:30",
		Notes:        "Forgot to clock in",
	}); err != nil {
		t.Fatalf("createTimePunchCorrection: %v", err)
	}
	corrections, err := listTimePunchCorrections(db, locationID)
	if err != nil {
		t.Fatalf("listTimePunchCorrections: %v", err)
	}
	if len(corrections) != 1 || corrections[0].EmployeeName != "Blanco, John" || corrections[0].StartTime != "08:00" {
		t.Fatalf("unexpected corrections: %#v", corrections)
	}
	if err := deleteTimePunchCorrection(db, locationID, corrections[0].ID); err != nil {
		t.Fatalf("deleteTimePunchCorrection: %v", err)
	}
	corrections, err = listTimePunchCorrections(db, locationID)
	if err != nil {
		t.Fatalf("listTimePunchCorrections after delete: %v", err)
	}
	if len(corrections) != 0 {
		t.Fatalf("expected correction to be deleted, got %#v", corrections)
	}
}

func TestUnknownClockInPINDoesNotCreateCorrection(t *testing.T) {
	db, err := openDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	locationID, err := createLocation(db, "Southroads", "03394", "southroads@example.com")
	if err != nil {
		t.Fatalf("createLocation: %v", err)
	}
	if _, err := getEmployeeByClockInPIN(db, locationID, "00000"); err == nil {
		t.Fatal("expected missing PIN to return an error")
	}
	corrections, err := listTimePunchCorrections(db, locationID)
	if err != nil {
		t.Fatalf("listTimePunchCorrections: %v", err)
	}
	if len(corrections) != 0 {
		t.Fatalf("missing PIN should not create corrections: %#v", corrections)
	}
}

func TestPublicTimePunchCorrectionLookupMissingPINShowsEmailFallback(t *testing.T) {
	db, err := openDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	locationID, err := createLocation(db, "Southroads", "03394", "southroads@example.com")
	if err != nil {
		t.Fatalf("createLocation: %v", err)
	}
	loc, err := getLocation(db, locationID)
	if err != nil {
		t.Fatalf("getLocation: %v", err)
	}
	app := &App{db: db, dataDir: t.TempDir()}
	form := url.Values{"step": {"lookup"}, "clock_in_pin": {"404404"}}
	var reqBody bytes.Buffer
	writer := multipart.NewWriter(&reqBody)
	for key, values := range form {
		for _, value := range values {
			if err := writer.WriteField(key, value); err != nil {
				t.Fatalf("write multipart field: %v", err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest("POST", "/forms/time-punch/"+loc.TimePunchToken, &reqBody)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.SetPathValue("token", loc.TimePunchToken)
	rec := httptest.NewRecorder()
	app.publicTimePunchCorrectionSubmit(rec, req)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "Email your time punch correction to") || strings.Contains(body, `name="business_date"`) {
		t.Fatalf("missing PIN should show email fallback without correction fields, status = %d body: %s", rec.Code, body)
	}
}

func TestPublicTimePunchCorrectionLookupRoutesByProfilePhoto(t *testing.T) {
	db, err := openDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	locationID, err := createLocation(db, "Southroads", "03394", "southroads@example.com")
	if err != nil {
		t.Fatalf("createLocation: %v", err)
	}
	loc, err := getLocation(db, locationID)
	if err != nil {
		t.Fatalf("getLocation: %v", err)
	}
	_, err = db.Exec(`INSERT INTO employees (location_id, employee_name, employee_number, job, employee_status, location_latest_start_date, clock_in_pin, profile_photo_data_url)
		VALUES (?, 'Blanco, John', '12-1083836', 'Team Member', 'Active', '2026-01-01', '99129', 'data:image/jpeg;base64,cGhvdG8=')`, locationID)
	if err != nil {
		t.Fatalf("insert employee with photo: %v", err)
	}
	_, err = db.Exec(`INSERT INTO employees (location_id, employee_name, employee_number, job, employee_status, location_latest_start_date, clock_in_pin)
		VALUES (?, 'Rivera, Maria', '12-1083837', 'Team Member', 'Active', '2026-01-01', '55129')`, locationID)
	if err != nil {
		t.Fatalf("insert employee without photo: %v", err)
	}
	app := &App{db: db, dataDir: t.TempDir()}
	postLookup := func(form url.Values) *httptest.ResponseRecorder {
		var reqBody bytes.Buffer
		writer := multipart.NewWriter(&reqBody)
		for key, values := range form {
			for _, value := range values {
				if err := writer.WriteField(key, value); err != nil {
					t.Fatalf("write multipart field: %v", err)
				}
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("close multipart writer: %v", err)
		}
		req := httptest.NewRequest("POST", "/forms/time-punch/"+loc.TimePunchToken, &reqBody)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.SetPathValue("token", loc.TimePunchToken)
		rec := httptest.NewRecorder()
		app.publicTimePunchCorrectionSubmit(rec, req)
		return rec
	}

	withPhoto := url.Values{"step": {"lookup"}, "clock_in_pin": {"99129"}}
	rec := postLookup(withPhoto)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `name="step" value="correction"`) || !strings.Contains(body, `name="business_date"`) {
		t.Fatalf("employee with photo should route to correction step, status = %d body: %s", rec.Code, body)
	}

	withoutPhoto := url.Values{"step": {"lookup"}, "clock_in_pin": {"55129"}}
	rec = postLookup(withoutPhoto)
	body = rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `name="step" value="photo"`) || !strings.Contains(body, `name="profile_photo_data_url"`) || strings.Contains(body, `name="business_date"`) {
		t.Fatalf("employee without photo should route to photo step only, status = %d body: %s", rec.Code, body)
	}
}

func TestParseDaypartActivityPDFSingleDay(t *testing.T) {
	data, err := os.ReadFile("daypart_activity_singleday.pdf")
	if err != nil {
		t.Skip("daypart_activity_singleday.pdf fixture is not present")
	}
	report, err := parseDaypartActivityPDF(multipartFile{Reader: bytes.NewReader(data)}, &multipart.FileHeader{Filename: "daypart_activity_singleday.pdf"})
	if err != nil {
		t.Fatalf("parseDaypartActivityPDF: %v", err)
	}
	if report.BusinessDate != "2026-06-08" {
		t.Fatalf("unexpected business date: %s", report.BusinessDate)
	}
	if report.Dayparts["Breakfast"] != 448691 || report.Dayparts["Lunch"] != 1338179 || report.Dayparts["Afternoon"] != 544777 || report.Dayparts["Dinner"] != 954304 {
		t.Fatalf("unexpected daypart totals: %#v", report.Dayparts)
	}
	if report.Destinations["DRIVE THRU"] != 1675920 || sumSalesMap(report.Destinations) != 3285951 {
		t.Fatalf("unexpected destination totals: %#v", report.Destinations)
	}
}

func TestParseDaypartActivityPDFRejectsMultiDay(t *testing.T) {
	data, err := os.ReadFile("daypart_activity_multiday.pdf")
	if err != nil {
		t.Skip("daypart_activity_multiday.pdf fixture is not present")
	}
	if _, err := parseDaypartActivityPDF(multipartFile{Reader: bytes.NewReader(data)}, &multipart.FileHeader{Filename: "daypart_activity_multiday.pdf"}); err == nil {
		t.Fatal("expected multi-day report to be rejected")
	}
}

func TestSaveAndListDailySales(t *testing.T) {
	db, err := openDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	locationID, err := createLocation(db, "Southroads", "03394", "southroads@example.com")
	if err != nil {
		t.Fatalf("createLocation: %v", err)
	}
	report := DaypartSalesReport{
		BusinessDate: "2026-06-08",
		Dayparts: map[string]int64{
			"Breakfast": 100,
			"Lunch":     200,
			"Afternoon": 300,
			"Dinner":    400,
		},
		Destinations: map[string]int64{
			"CARRY OUT":    100,
			"DELIVERY":     0,
			"DINE IN":      100,
			"DRIVE THRU":   800,
			"M-CARRYOUT":   0,
			"M-DINEIN":     0,
			"M-DRIVE-THRU": 0,
			"ON DEMAND":    0,
			"PICKUP":       0,
		},
	}
	if err := saveDailySales(db, locationID, report); err != nil {
		t.Fatalf("saveDailySales: %v", err)
	}
	rows, err := listDailySales(db, locationID, "2026-06-08", "2026-06-08")
	if err != nil {
		t.Fatalf("listDailySales: %v", err)
	}
	if len(rows) != 1 || rows[0].TotalCents != 1000 || rows[0].Dayparts["Dinner"] != 400 || rows[0].Destinations["DRIVE THRU"] != 800 {
		t.Fatalf("unexpected stored sales: %#v", rows)
	}
}

func TestMissingSalesDatesSkipsSundays(t *testing.T) {
	start := time.Date(2026, time.June, 7, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, time.June, 9, 0, 0, 0, 0, time.Local)
	missing := missingSalesDates(start, end, []DailySales{{BusinessDate: "2026-06-08"}})
	if len(missing) != 1 || missing[0] != "2026-06-09" {
		t.Fatalf("unexpected missing dates: %#v", missing)
	}
}

func TestMissingSalesDatesIgnoresTodayAndFutureDates(t *testing.T) {
	start := time.Date(2026, time.June, 15, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, time.June, 20, 0, 0, 0, 0, time.Local)
	today := time.Date(2026, time.June, 17, 0, 0, 0, 0, time.Local)
	missing := missingSalesDatesBefore(start, end, nil, today)
	if len(missing) != 2 || missing[0] != "2026-06-15" || missing[1] != "2026-06-16" {
		t.Fatalf("unexpected missing dates: %#v", missing)
	}
	count := requiredSalesDateCountBefore(start, end, today)
	if count != 2 {
		t.Fatalf("expected 2 required past dates, got %d", count)
	}
}

func TestMissingLaborDatesIgnoresTodayAndFutureDates(t *testing.T) {
	start := time.Date(2026, time.June, 15, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, time.June, 20, 0, 0, 0, 0, time.Local)
	today := time.Date(2026, time.June, 17, 0, 0, 0, 0, time.Local)
	missing := missingLaborDatesBefore(start, end, []DailyLabor{{BusinessDate: "2026-06-15"}}, today)
	if len(missing) != 1 || missing[0] != "2026-06-16" {
		t.Fatalf("unexpected missing dates: %#v", missing)
	}
}

func TestMatchPinEmployeeIDHandlesExactReportName(t *testing.T) {
	employees := []pinImportEmployee{{ID: 1, Name: "Vasquez, Rafael"}}
	employees[0].Keys = pinNameKeys(employees[0].Name)
	gotID, ok := matchPinEmployeeID(employees, "Vasquez, Rafael")
	if !ok || gotID != 1 {
		t.Fatalf("matchPinEmployeeID returned %d, %v; want 1, true", gotID, ok)
	}
}

func TestMatchPinEmployeeIDHandlesReportNameVariants(t *testing.T) {
	employees := []pinImportEmployee{
		{ID: 1, Name: "Angeles Escobar, James M"},
		{ID: 2, Name: "Baker, Ramond Manley (Ray)"},
		{ID: 3, Name: "Boone, Zion J"},
		{ID: 4, Name: "De La Cruz, Stephanie A"},
		{ID: 5, Name: "De Leon, Maria V. (Vanessa)"},
	}
	for i := range employees {
		employees[i].Keys = pinNameKeys(employees[i].Name)
	}
	cases := map[string]int64{
		"Angeles Escobar, James":   1,
		"Baker, Ramond (Ray)":      2,
		"Boone, Zion":              3,
		"De La Cruz, Stephanie":    4,
		"De Leon, Maria (Vanessa)": 5,
	}
	for reportName, wantID := range cases {
		gotID, ok := matchPinEmployeeID(employees, reportName)
		if !ok || gotID != wantID {
			t.Fatalf("matchPinEmployeeID(%q) = %d, %v; want %d, true", reportName, gotID, ok, wantID)
		}
	}
}

func TestMatchPinEmployeeIDSkipsAmbiguousNames(t *testing.T) {
	employees := []pinImportEmployee{
		{ID: 1, Name: "Smith, John A"},
		{ID: 2, Name: "Smith, John B"},
	}
	for i := range employees {
		employees[i].Keys = pinNameKeys(employees[i].Name)
	}
	if gotID, ok := matchPinEmployeeID(employees, "Smith, John"); ok || gotID != 0 {
		t.Fatalf("ambiguous name matched employee %d", gotID)
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

func TestSaveDailyLaborPersistsSelectedDate(t *testing.T) {
	db, err := openDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	locationID, err := createLocation(db, "Southroads", "03394", "southroads@example.com")
	if err != nil {
		t.Fatalf("createLocation: %v", err)
	}
	report, err := parseTimePunchText(`Employee Time Detail
Store
From Monday, May 11, 2026 through Tuesday, May 12, 2026
Baker, Ramond Manley (Ray)
Mon, 05/11/2026 8:00a 1:00p 5:00 Regular $15.00 5:00 $75.00 $75.00
Mon, 05/11/2026 1:00p 3:00p 2:00 Overtime $22.50 2:00 $45.00 $45.00
Tue, 05/12/2026 8:00a 2:00p 6:00 Regular $15.00 6:00 $90.00 $90.00
`)
	if err != nil {
		t.Fatalf("parseTimePunchText: %v", err)
	}
	report.Employees[0].Role = "Trainer"
	report.Employees[0].Department = "Front of House"
	report.Employees[0].Job = "Team Member"
	if err := saveDailyLabor(db, locationID, "2026-05-11", report); err != nil {
		t.Fatalf("saveDailyLabor: %v", err)
	}
	labor, err := listDailyLabor(db, locationID, "2026-05-01", "2026-05-31")
	if err != nil {
		t.Fatalf("listDailyLabor: %v", err)
	}
	if len(labor) != 1 {
		t.Fatalf("expected one stored labor day, got %#v", labor)
	}
	if labor[0].BusinessDate != "2026-05-11" || labor[0].TotalMinutes != 420 || labor[0].OvertimeMinutes != 120 || labor[0].TotalWagesCents != 12000 {
		t.Fatalf("unexpected stored labor day: %#v", labor[0])
	}
	if labor[0].Roles["Trainer"].WagesCents != 12000 || labor[0].Roles["Trainer"].OvertimeWagesCents != 4500 || labor[0].Jobs["Team Member"].Minutes != 420 {
		t.Fatalf("expected stored breakdowns, got %#v", labor[0])
	}
}

func TestLaborSummaryShowsRegularAndOvertimeTotals(t *testing.T) {
	report, err := parseTimePunchText(`Employee Time Detail
Store
From Monday, May 11, 2026 through Monday, May 11, 2026
Baker, Ramond Manley (Ray)
Mon, 05/11/2026 8:00a 1:00p 5:00 Regular $15.00 5:00 $75.00 $75.00
Mon, 05/11/2026 1:00p 3:00p 2:00 Overtime $22.50 2:00 $45.00 $45.00
`)
	if err != nil {
		t.Fatalf("parseTimePunchText: %v", err)
	}
	summary := laborSummary(report)
	if len(summary) < 2 {
		t.Fatalf("expected labor summary rows, got %#v", summary)
	}
	if summary[0].Hours != "7.00" || summary[0].Dollars != "Regular 5.00" || summary[0].Detail != "Overtime 2.00" {
		t.Fatalf("expected regular and overtime hour summary, got %#v", summary[0])
	}
	if summary[1].Hours != "$120.00" || summary[1].Dollars != "Regular $75.00" || summary[1].Detail != "Overtime $45.00" {
		t.Fatalf("expected regular and overtime dollar summary, got %#v", summary[1])
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

func TestParseTimePunchPDFSouthroadsOvertimeTotals(t *testing.T) {
	data, err := os.ReadFile("sr_tp.pdf")
	if err != nil {
		t.Skip("sr_tp.pdf fixture is not present")
	}
	report, err := parseTimePunchPDF(multipartFile{Reader: bytes.NewReader(data)}, &multipart.FileHeader{Filename: "sr_tp.pdf"})
	if err != nil {
		t.Fatalf("parseTimePunchPDF returned error: %v", err)
	}
	if report.GrandTotals.Minutes != 307177 {
		t.Fatalf("expected total hours 5119:37, got %#v", report.GrandTotals)
	}
	if report.GrandTotals.OvertimeMinutes != 16168 {
		t.Fatalf("expected overtime hours 269:28, got %#v", report.GrandTotals)
	}
	if report.GrandTotals.RegularMinutes != 291009 {
		t.Fatalf("expected regular hours 4850:09, got %#v", report.GrandTotals)
	}
	if report.GrandTotals.WagesCents != 8227527 {
		t.Fatalf("expected total wages $82,275.27, got %#v", report.GrandTotals)
	}
	if report.GrandTotals.RegularWagesCents != 7506345 {
		t.Fatalf("expected regular wages $75,063.45, got %#v", report.GrandTotals)
	}
	if report.GrandTotals.OvertimeWagesCents != 721183 {
		t.Fatalf("expected overtime wages $7,211.83, got %#v", report.GrandTotals)
	}
	summary := laborSummary(report)
	if len(summary) < 2 || summary[0].Dollars != "Regular 4850.15" || summary[0].Detail != "Overtime 269.47" || summary[1].Dollars != "Regular $75063.45" || summary[1].Detail != "Overtime $7211.83" {
		t.Fatalf("unexpected overtime summary: %#v", summary)
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

func TestTimePunchWageRateDrivesSalaryLabor(t *testing.T) {
	report, err := parseTimePunchText(`Employee Time Detail
Store
From Thursday, May 1, 2026 through Sunday, May 31, 2026
Manager, Sally
Mon, 05/04/2026 8:00a 8:00a 0:00 Regular $7,500.00 0:00 $0.00 $0.00
Employee Totals 0:00 0:00 $0.00 $0.00
`)
	if err != nil {
		t.Fatalf("parseTimePunchText returned error: %v", err)
	}
	if len(report.Employees) != 1 || report.Employees[0].WageRateCents != 750000 || report.Employees[0].WagePayType != "salary" {
		t.Fatalf("expected parsed salary wage rate, got %#v", report.Employees)
	}
	applyEmployeeAssignments(&report, []Employee{{
		EmployeeName:   "Manager, Sally",
		EmployeeNumber: "99",
		Job:            "Director",
		WageRateCents:  int64Ptr(750000),
		WagePayType:    "salary",
	}})
	finalizeLaborReport(&report, nil)
	if report.GrandTotals.WagesCents != 750000 {
		t.Fatalf("expected full monthly salary in labor total, got %s", formatDollars(report.GrandTotals.WagesCents))
	}
	rows := laborJobRows(report)
	if len(rows) != 1 || rows[0].Job != "Director" || rows[0].Dollars != "$7500.00" {
		t.Fatalf("expected salary in job labor row, got %#v", rows)
	}
}

func TestSalaryEmployeePunchedHoursArePreserved(t *testing.T) {
	report, err := parseTimePunchText(`Employee Time Detail
Store
From Thursday, May 1, 2026 through Sunday, May 31, 2026
Manager, Sally
Mon, 05/04/2026 8:00a 1:00p 5:00 Regular $7,500.00 5:00 $0.00 $0.00
Employee Totals 5:00 5:00 $0.00 $0.00
`)
	if err != nil {
		t.Fatalf("parseTimePunchText returned error: %v", err)
	}
	applyEmployeeAssignments(&report, []Employee{{
		EmployeeName:   "Manager, Sally",
		EmployeeNumber: "99",
		Job:            "Director",
		WageRateCents:  int64Ptr(750000),
		WagePayType:    "salary",
	}})
	finalizeLaborReport(&report, nil)
	if len(report.Employees) != 1 {
		t.Fatalf("expected salary employee to remain, got %#v", report.Employees)
	}
	if report.Employees[0].Totals.Minutes != 300 {
		t.Fatalf("expected punched salary hours to remain, got %#v", report.Employees[0].Totals)
	}
	if report.Employees[0].Totals.WagesCents != 750000 {
		t.Fatalf("expected salary labor dollars, got %s", formatDollars(report.Employees[0].Totals.WagesCents))
	}
	rows := laborEmployeeRows(report)
	if len(rows) != 1 || rows[0].Hours != "5.00" || rows[0].Dollars != "$7500.00" {
		t.Fatalf("expected salary employee row with hours and salary dollars, got %#v", rows)
	}
}

func TestSalaryLaborUsesReportDayCount(t *testing.T) {
	days := salaryLaborDays("2026-05-01", "2026-05-01", 750000)
	total := sumEmployeeDays(LaborEmployee{Days: days})
	if total.WagesCents != 24194 {
		t.Fatalf("expected one May day of salary, got %s", formatDollars(total.WagesCents))
	}
}

func TestStoredSalaryEmployeeCountsWhenMissingFromTimePunchRows(t *testing.T) {
	report := TimePunchReport{StartDate: "2026-05-01", EndDate: "2026-05-31"}
	finalizeLaborReport(&report, []Employee{{
		EmployeeName:   "Manager, Sally",
		EmployeeNumber: "99",
		Job:            "Director",
		WageRateCents:  int64Ptr(750000),
		WagePayType:    "salary",
	}})
	if len(report.Employees) != 1 || report.GrandTotals.WagesCents != 750000 {
		t.Fatalf("expected missing salary employee to count, got %#v", report)
	}
}

func TestLaborUploadWageUpdateAndExclusion(t *testing.T) {
	db, err := openDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	locationID, err := createLocation(db, "Southroads", "03394", "southroads@example.com")
	if err != nil {
		t.Fatalf("createLocation: %v", err)
	}
	_, err = db.Exec(`INSERT INTO employees (location_id, employee_name, employee_number, job, employee_status, location_latest_start_date)
		VALUES (?, ?, ?, ?, ?, ?)`, locationID, "Manager, Sally", "99", "Director", "Active", "2024-10-01")
	if err != nil {
		t.Fatalf("insert employee: %v", err)
	}
	report, err := parseTimePunchText(`Employee Time Detail
Store
From Thursday, May 1, 2026 through Sunday, May 31, 2026
Manager, Sally
Mon, 05/04/2026 8:00a 8:00a 0:00 Regular $7,500.00 0:00 $0.00 $0.00
Employee Totals 0:00 0:00 $0.00 $0.00
`)
	if err != nil {
		t.Fatalf("parseTimePunchText returned error: %v", err)
	}
	employees, err := listEmployees(db, locationID)
	if err != nil {
		t.Fatalf("listEmployees: %v", err)
	}
	if err := updateEmployeeWagesFromReport(db, report, employees); err != nil {
		t.Fatalf("updateEmployeeWagesFromReport: %v", err)
	}
	employee, err := getEmployee(db, locationID, "99")
	if err != nil {
		t.Fatalf("getEmployee: %v", err)
	}
	if employee.WageRateCents == nil || *employee.WageRateCents != 750000 || employee.WagePayType != "salary" {
		t.Fatalf("expected stored salary wage details, got %#v", employee)
	}
	if _, err := assignEmployeeLaborExclusion(db, locationID, []int64{employee.ID}, true); err != nil {
		t.Fatalf("assignEmployeeLaborExclusion: %v", err)
	}
	employees, err = listEmployees(db, locationID)
	if err != nil {
		t.Fatalf("listEmployees: %v", err)
	}
	applyEmployeeAssignments(&report, employees)
	finalizeLaborReport(&report, employees)
	if len(report.Employees) != 0 || report.GrandTotals.WagesCents != 0 {
		t.Fatalf("excluded employee should not count toward labor: %#v", report)
	}
}

func TestCalendarDaysBuildsStableMonthGrid(t *testing.T) {
	month := time.Date(2026, time.June, 1, 0, 0, 0, 0, time.Local)
	today := time.Date(2026, time.June, 13, 12, 0, 0, 0, time.Local)
	days := calendarDays(month, today, map[string]bool{"2026-06-12": true, "2026-06-13": true}, map[string]bool{"2026-06-12": true})
	if len(days) != 42 {
		t.Fatalf("expected 42 calendar cells, got %d", len(days))
	}
	if days[0].Date != "2026-05-31" || days[1].Date != "2026-06-01" {
		t.Fatalf("unexpected calendar start: %#v %#v", days[0], days[1])
	}
	if !days[1].CurrentMonth || days[0].CurrentMonth {
		t.Fatalf("current month flags were not set correctly: %#v %#v", days[0], days[1])
	}
	var foundToday bool
	for _, day := range days {
		if day.Date == "2026-06-13" {
			foundToday = day.Today
		}
	}
	if !foundToday {
		t.Fatal("expected June 13 to be marked today")
	}
	if !days[13].HasSales {
		t.Fatal("expected June 13 to show imported sales")
	}
	if !days[12].Complete {
		t.Fatal("expected past day with sales and labor to be complete")
	}
	if !days[7].Sunday || days[7].SalesRequired || !days[7].Complete {
		t.Fatalf("expected current-month Sunday to be complete without required sales: %#v", days[7])
	}
	if days[13].Accessible {
		t.Fatal("expected today to not be accessible")
	}
	if days[0].SalesRequired {
		t.Fatal("expected outside-month day to not require sales")
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
				"JobOptions":       []string{"Team Member"},
				"AssignmentStatus": AssignmentStatus{},
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
			name: "location details",
			body: locationDetailsHTML,
			data: map[string]any{
				"Title":    "Employee Details",
				"Location": Location{ID: 1, Name: "Southroads", Number: "03394"},
				"Employees": []Employee{{
					EmployeeName:            "Blanco, John",
					EmployeeNumber:          "12-1083836",
					Job:                     "Team Member",
					WageRateCents:           int64Ptr(1500),
					WagePayType:             "hourly",
					EmployeeStatus:          "Active",
					LocationLatestStartDate: "2024-10-01",
					BirthDate:               stringPtr("1999-03-14"),
					ClockInPIN:              stringPtr("99129"),
				}},
				"Import": url.Values{},
			},
		},
		{
			name: "location pay",
			body: locationPayHTML,
			data: map[string]any{
				"Title":    "Employee Pay",
				"Location": Location{ID: 1, Name: "Southroads", Number: "03394"},
				"Employees": []Employee{{
					ID:                      1,
					EmployeeName:            "Blanco, John",
					EmployeeNumber:          "12-1083836",
					Job:                     "Team Member",
					WageRateCents:           int64Ptr(1500),
					WagePayType:             "hourly",
					EmployeeStatus:          "Active",
					LocationLatestStartDate: "2024-10-01",
				}},
				"Import": url.Values{},
			},
		},
		{
			name: "location calendar",
			body: locationCalendarHTML,
			data: map[string]any{
				"Title":      "Calendar",
				"Location":   Location{ID: 1, Name: "Southroads", Number: "03394"},
				"MonthLabel": "June 2026",
				"MonthValue": "2026-06",
				"PrevMonth":  "2026-05",
				"NextMonth":  "2026-07",
				"Days":       calendarDays(time.Date(2026, time.June, 1, 0, 0, 0, 0, time.Local), time.Date(2026, time.June, 13, 0, 0, 0, 0, time.Local), map[string]bool{"2026-06-13": true}, map[string]bool{}),
			},
		},
		{
			name: "location calendar day",
			body: locationCalendarDayHTML,
			data: map[string]any{
				"Title":       "Calendar Day",
				"Location":    Location{ID: 1, Name: "Southroads", Number: "03394"},
				"Date":        "2026-06-13",
				"DateLabel":   "Saturday, June 13, 2026",
				"MonthValue":  "2026-06",
				"BackToMonth": "/locations/1/calendar?month=2026-06",
				"PrevDayURL":  "/locations/1/calendar/2026-06-12",
				"NextDayURL":  "/locations/1/calendar/2026-06-14",
				"Sales": DailySales{
					BusinessDate: "2026-06-13",
					TotalCents:   1000,
					Dayparts:     map[string]int64{"Breakfast": 100, "Lunch": 200, "Afternoon": 300, "Dinner": 400},
					Destinations: map[string]int64{"CARRY OUT": 100, "DELIVERY": 0, "DINE IN": 100, "DRIVE THRU": 800, "M-CARRYOUT": 0, "M-DINEIN": 0, "M-DRIVE-THRU": 0, "ON DEMAND": 0, "PICKUP": 0},
				},
				"Import": url.Values{},
			},
		},
		{
			name: "location sales",
			body: locationSalesHTML,
			data: map[string]any{
				"Title":             "Sales",
				"Location":          Location{ID: 1, Name: "Southroads", Number: "03394"},
				"StartDate":         "2026-06-08",
				"EndDate":           "2026-06-08",
				"MissingDates":      []string{},
				"Complete":          true,
				"DailyRows":         []SalesDailyRow{{Date: "2026-06-08", Weekday: "Monday", TotalCents: 1000, Dayparts: salesRowsForLabels(map[string]int64{"Breakfast": 100, "Lunch": 200, "Afternoon": 300, "Dinner": 400}, salesDayparts)}},
				"DaypartRows":       salesRowsForLabels(map[string]int64{"Breakfast": 100, "Lunch": 200, "Afternoon": 300, "Dinner": 400}, salesDayparts),
				"DestinationRows":   salesRowsForLabels(map[string]int64{"CARRY OUT": 100, "DELIVERY": 0, "DINE IN": 100, "DRIVE THRU": 800, "M-CARRYOUT": 0, "M-DINEIN": 0, "M-DRIVE-THRU": 0, "ON DEMAND": 0, "PICKUP": 0}, salesDestinations),
				"DayOfWeekRows":     salesRowsForLabels(map[string]int64{"Monday": 1000}, []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}),
				"SelectedDateCount": 1,
			},
		},
		{
			name: "location edit",
			body: locationEditHTML,
			data: map[string]any{
				"Title":    "Edit",
				"Location": Location{ID: 1, Name: "Southroads", Number: "03394", Email: "southroads@example.com"},
				"Saved":    "1",
			},
		},
		{
			name: "location secret links",
			body: locationSecretLinksHTML,
			data: map[string]any{
				"Title":         "Secret Links",
				"Location":      Location{ID: 1, Name: "Southroads", Number: "03394", Email: "southroads@example.com"},
				"TimePunchLink": "https://hr.example.com/forms/time-punch/secret",
			},
		},
		{
			name: "location time punch corrections",
			body: locationTimePunchCorrectionsHTML,
			data: map[string]any{
				"Title":    "Time Punch Corrections",
				"Location": Location{ID: 1, Name: "Southroads", Number: "03394", Email: "southroads@example.com"},
				"Corrections": []TimePunchCorrection{{
					ID:             1,
					EmployeeName:   "Blanco, John",
					EmployeeNumber: "12-1083836",
					BusinessDate:   "2026-06-13",
					StartTime:      "08:00",
					EndTime:        "15:30",
					Notes:          "Forgot to clock in",
					CreatedAt:      time.Date(2026, time.June, 13, 16, 0, 0, 0, time.Local),
				}},
			},
		},
		{
			name: "public time punch correction",
			body: publicTimePunchCorrectionHTML,
			data: map[string]any{
				"Title":              "Time Punch Correction",
				"LoggedOut":          true,
				"ShowLanguageToggle": true,
				"Location":           Location{ID: 1, Name: "Southroads", Number: "03394", Email: "southroads@example.com"},
				"Today":              "2026-06-13",
				"Step":               "pin",
				"Form":               TimePunchCorrection{},
			},
		},
		{
			name: "public employee photo",
			body: publicEmployeePhotoHTML,
			data: map[string]any{
				"Title":     "Profile Photo",
				"LoggedOut": true,
				"Location":  Location{ID: 1, Name: "Southroads", Number: "03394", Email: "southroads@example.com"},
				"Employee":  Employee{ID: 1, EmployeeName: "Blanco, John", EmployeeNumber: "12-1083836"},
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

func TestMissingDateNoticesLinkToCalendarDays(t *testing.T) {
	tests := []struct {
		name string
		body string
		data map[string]any
		want string
	}{
		{
			name: "sales",
			body: locationSalesHTML,
			data: map[string]any{
				"Title":             "Sales",
				"Location":          Location{ID: 2, Name: "Southroads", Number: "03394"},
				"StartDate":         "2026-06-08",
				"EndDate":           "2026-06-09",
				"MissingDates":      []string{"2026-06-09"},
				"Complete":          false,
				"SelectedDateCount": 2,
			},
			want: `<a class="missing-date-link" href="/locations/2/calendar/2026-06-09">Tuesday, June 9, 2026</a>`,
		},
		{
			name: "labor",
			body: laborHTML,
			data: map[string]any{
				"Title":            "Labor",
				"SelectedLocation": Location{ID: 2, Name: "Southroads", Number: "03394"},
				"StartDate":        "2026-06-08",
				"EndDate":          "2026-06-09",
				"MissingDates":     []string{"2026-06-09"},
				"Complete":         false,
			},
			want: `<a class="missing-date-link" href="/locations/2/calendar/2026-06-09">Tuesday, June 9, 2026</a>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, err := template.New("layout").Funcs(templateFuncs()).Parse(layoutHTML + tt.body)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			var buf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&buf, "layout", tt.data); err != nil {
				t.Fatalf("ExecuteTemplate: %v", err)
			}
			if !strings.Contains(buf.String(), tt.want) {
				t.Fatalf("rendered missing date link not found; want %q in:\n%s", tt.want, buf.String())
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

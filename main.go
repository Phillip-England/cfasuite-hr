package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"mime/multipart"
	"net/http"
	"net/mail"
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
	case "api-key-env":
		cmdAPIKeyEnv(os.Args[2:])
	case "set-api-key":
		cmdSetAPIKey(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Printf(`%s - Chick-fil-A HR admin app and employee API

Quick start:
  cfasuite-hr init
  cfasuite-hr set-admin -username admin -password change-me
  cfasuite-hr serve

Run the web app:
  cfasuite-hr serve [-addr :8217] [-db path]
      Start the admin UI and API server.

Database:
  cfasuite-hr init [-db path]
      Create or migrate the SQLite database and print its path.
  cfasuite-hr db path [-db path]
      Print the active SQLite database path.
  cfasuite-hr db reset -yes [-db path]
      Delete and recreate the SQLite database. This destroys app data.

Admin access:
  cfasuite-hr set-admin -username admin -password secret [-db path]
      Save browser admin credentials in SQLite.
  cfasuite-hr admin-env -username admin -password secret
      Print shell exports for admin credentials instead of saving them.

API tokens:
  cfasuite-hr token create -name "Reporting" [-db path]
      Create an API token. Copy the token when it is printed.
  cfasuite-hr token list [-db path]
      List token ids, names, prefixes, creation times, and last use.
  cfasuite-hr token delete -id 1 [-db path]
      Delete an API token.

API client environment:
  cfasuite-hr set-api-key cfa_... [-env-file ~/.zshrc]
      Save CFASUITE_HR_API_KEY into your shell startup file.
  cfasuite-hr api-key-env -api-key cfa_...
      Print an export command for CFASUITE_HR_API_KEY.

Environment:
  CFASUITE_DB_PATH          SQLite database file path.
  CFASUITE_DATA_DIR         App-owned data directory for db defaults and temp files.
  CFASUITE_ADDR             HTTP listen address, default :8217.
  CFASUITE_ADMIN_USERNAME   Admin username when not stored in SQLite.
  CFASUITE_ADMIN_PASSWORD   Admin password when not stored in SQLite.
  CFASUITE_SESSION_SECRET   Secret used to sign browser sessions.
  CFASUITE_HR_API_KEY       API token used by SDK clients and scripts.
  CFASUITE_HR_ENV_FILE      Default env file target for set-api-key.

Examples:
  cfasuite-hr token create -name "Payroll Export"
  cfasuite-hr set-api-key cfa_your_token
  source ~/.zshrc
  curl -H "Authorization: Bearer $CFASUITE_HR_API_KEY" http://localhost:8217/api/v1/locations
`, appName)
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
	id, err := createLocation(a.db, r.FormValue("name"), r.FormValue("number"), r.FormValue("email"))
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

func (a *App) employeeProfile(w http.ResponseWriter, r *http.Request) {
	locationID, err := pathID(r, "locationID")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	loc, err := getLocation(a.db, locationID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	employee, err := getEmployeeByID(a.db, locationID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a.render(w, employee.EmployeeName, employeeProfileHTML, map[string]any{
		"Location": loc,
		"Employee": employee,
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
	goal, err := getMonthlyProductivityGoal(a.db, id, month.Format("2006-01"))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, loc.Name+" Calendar", locationCalendarHTML, map[string]any{
		"Location":              loc,
		"MonthLabel":            month.Format("January 2006"),
		"MonthValue":            month.Format("2006-01"),
		"PrevMonth":             month.AddDate(0, -1, 0).Format("2006-01"),
		"NextMonth":             month.AddDate(0, 1, 0).Format("2006-01"),
		"ProductivityGoalValue": goal.GoalDisplayValue,
		"Days":                  calendarDays(month, time.Now(), salesDates, laborDates),
	})
}

func (a *App) locationProductivityGoalUpdate(w http.ResponseWriter, r *http.Request) {
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
	month, err := monthValueFromString(r.FormValue("month"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	goalValue := strings.TrimSpace(r.FormValue("productivity_goal"))
	if goalValue == "" {
		if err := deleteMonthlyProductivityGoal(a.db, id, month); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/locations/%d/calendar?month=%s", id, month), http.StatusSeeOther)
		return
	}
	goal, err := parseProductivityGoal(goalValue)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := saveMonthlyProductivityGoal(a.db, id, month, goal); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/locations/%d/calendar?month=%s", id, month), http.StatusSeeOther)
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
	benchmark := salesBenchmark(sales)
	a.render(w, loc.Name+" Sales", locationSalesHTML, map[string]any{
		"Location":          loc,
		"StartDate":         start.Format("2006-01-02"),
		"EndDate":           end.Format("2006-01-02"),
		"MissingDates":      missing,
		"Complete":          len(missing) == 0,
		"DailyRows":         salesDailyRows(sales),
		"SalesChart":        salesChartPoints(sales, benchmark.AverageCents, start, end),
		"SalesBenchmark":    benchmark,
		"DaypartRows":       aggregateSalesRows(sales, "daypart"),
		"DestinationRows":   aggregateSalesRows(sales, "destination"),
		"DayOfWeekRows":     dayOfWeekSalesRows(sales),
		"SelectedDateCount": requiredSalesDateCount(start, end),
	})
}

func (a *App) locationProductivity(w http.ResponseWriter, r *http.Request) {
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
	productivityRange, err := productivityRangeFromRequest(r, startOfDay(time.Now()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	report, err := productivityReportForRange(a.db, id, productivityRange, startOfDay(time.Now()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.render(w, loc.Name+" Productivity", locationProductivityHTML, map[string]any{
		"Location": loc,
		"Report":   report,
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
	if err := updateLocation(a.db, id, r.FormValue("name"), r.FormValue("number"), r.FormValue("email")); err != nil {
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

func (a *App) docsPage(w http.ResponseWriter, r *http.Request) {
	a.render(w, "API Docs", docsHTML, map[string]any{"BaseURL": absoluteBaseURL(r)})
}

func listLocations(db *sql.DB) ([]Location, error) {
	rows, err := db.Query(`SELECT l.id, l.name, l.number, l.email, l.created_at, l.updated_at, COUNT(e.id)
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
		if err := rows.Scan(&loc.ID, &loc.Name, &loc.Number, &loc.Email, &created, &updated, &loc.Employees); err != nil {
			return nil, err
		}
		loc.CreatedAt = parseTime(created)
		loc.UpdatedAt = parseTime(updated)
		locations = append(locations, loc)
	}
	return locations, rows.Err()
}

func createLocation(db *sql.DB, name, number, email string) (int64, error) {
	name, number, email, err := validateLocationInput(name, number, email)
	if err != nil {
		return 0, err
	}
	res, err := db.Exec(`INSERT INTO locations (name, number, email) VALUES (?, ?, ?)`, name, number, email)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func updateLocation(db *sql.DB, id int64, name, number, email string) error {
	name, number, email, err := validateLocationInput(name, number, email)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE locations SET name = ?, number = ?, email = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, name, number, email, id)
	return err
}

func validateLocationInput(name, number, email string) (string, string, string, error) {
	name = strings.TrimSpace(name)
	number = strings.TrimSpace(number)
	email = strings.TrimSpace(email)
	if name == "" || number == "" || email == "" {
		return "", "", "", errors.New("name, number, and email are required")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return "", "", "", errors.New("store email must be valid")
	}
	return name, number, email, nil
}

func getLocation(db *sql.DB, id int64) (Location, error) {
	var loc Location
	var created, updated string
	err := db.QueryRow(`SELECT id, name, number, email, created_at, updated_at FROM locations WHERE id = ?`, id).Scan(&loc.ID, &loc.Name, &loc.Number, &loc.Email, &created, &updated)
	loc.CreatedAt = parseTime(created)
	loc.UpdatedAt = parseTime(updated)
	return loc, err
}

func getLocationByNumber(db *sql.DB, number string) (Location, error) {
	var loc Location
	var created, updated string
	err := db.QueryRow(`SELECT id, name, number, email, created_at, updated_at FROM locations WHERE number = ?`, number).Scan(&loc.ID, &loc.Name, &loc.Number, &loc.Email, &created, &updated)
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

func employeeSelectSQL() string {
	return `SELECT e.id, e.location_id, e.employee_name, e.employee_number, e.job, e.role_id, r.name, e.department_id, d.name, e.wage_rate_cents, e.wage_pay_type, e.exclude_from_labor, e.employee_status, e.location_latest_start_date, e.birth_date, e.clock_in_pin, e.created_at, e.updated_at
		FROM employees e
		LEFT JOIN roles r ON r.id = e.role_id
		LEFT JOIN departments d ON d.id = e.department_id`
}

func listEmployees(db *sql.DB, locationID int64) ([]Employee, error) {
	rows, err := db.Query(employeeSelectSQL()+` WHERE e.location_id = ?
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
	row := db.QueryRow(employeeSelectSQL()+` WHERE e.location_id = ? AND e.employee_number = ?`, locationID, number)
	return scanEmployee(row)
}

func getEmployeeByID(db *sql.DB, locationID, id int64) (Employee, error) {
	row := db.QueryRow(employeeSelectSQL()+` WHERE e.location_id = ? AND e.id = ?`, locationID, id)
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

func safePathName(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
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
	tmpDir, err := appTempDir()
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(tmpDir, "daypart-*.pdf")
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

func getMonthlyProductivityGoal(db *sql.DB, locationID int64, month string) (MonthlyProductivityGoal, error) {
	var goal MonthlyProductivityGoal
	var created, updated string
	err := db.QueryRow(`SELECT location_id, month, goal_basis_points, created_at, updated_at
		FROM monthly_productivity_goals
		WHERE location_id = ? AND month = ?`, locationID, month).
		Scan(&goal.LocationID, &goal.Month, &goal.GoalBasisPoints, &created, &updated)
	if err != nil {
		return MonthlyProductivityGoal{}, err
	}
	goal.GoalDisplayValue = formatProductivityGoal(goal.GoalBasisPoints)
	goal.CreatedAt = parseTime(created)
	goal.UpdatedAt = parseTime(updated)
	return goal, nil
}

func listMonthlyProductivityGoals(db *sql.DB, locationID int64, startMonth string, endMonth string) (map[string]MonthlyProductivityGoal, error) {
	rows, err := db.Query(`SELECT location_id, month, goal_basis_points, created_at, updated_at
		FROM monthly_productivity_goals
		WHERE location_id = ? AND month BETWEEN ? AND ?
		ORDER BY month`, locationID, startMonth, endMonth)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	goals := map[string]MonthlyProductivityGoal{}
	for rows.Next() {
		var goal MonthlyProductivityGoal
		var created, updated string
		if err := rows.Scan(&goal.LocationID, &goal.Month, &goal.GoalBasisPoints, &created, &updated); err != nil {
			return nil, err
		}
		goal.GoalDisplayValue = formatProductivityGoal(goal.GoalBasisPoints)
		goal.CreatedAt = parseTime(created)
		goal.UpdatedAt = parseTime(updated)
		goals[goal.Month] = goal
	}
	return goals, rows.Err()
}

func saveMonthlyProductivityGoal(db *sql.DB, locationID int64, month string, goalBasisPoints int64) error {
	_, err := db.Exec(`INSERT INTO monthly_productivity_goals (location_id, month, goal_basis_points, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(location_id, month) DO UPDATE SET
			goal_basis_points = excluded.goal_basis_points,
			updated_at = CURRENT_TIMESTAMP`, locationID, month, goalBasisPoints)
	return err
}

func deleteMonthlyProductivityGoal(db *sql.DB, locationID int64, month string) error {
	_, err := db.Exec(`DELETE FROM monthly_productivity_goals WHERE location_id = ? AND month = ?`, locationID, month)
	return err
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
	now := startOfDay(time.Now())
	defaultEnd := now.AddDate(0, 0, -1)
	startValue := strings.TrimSpace(r.URL.Query().Get("start"))
	endValue := strings.TrimSpace(r.URL.Query().Get("end"))
	if startValue == "" {
		startValue = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local).Format("2006-01-02")
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
	return start, end, nil
}

func missingSalesDates(start, end time.Time, sales []DailySales) []string {
	return missingSalesDatesBefore(start, end, sales, startOfDay(time.Now()))
}

func missingSalesDatesBefore(start, end time.Time, sales []DailySales, today time.Time) []string {
	present := map[string]bool{}
	for _, sale := range sales {
		present[sale.BusinessDate] = true
	}
	var missing []string
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if !d.Before(today) {
			continue
		}
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
	return requiredSalesDateCountBefore(start, end, startOfDay(time.Now()))
}

func requiredSalesDateCountBefore(start, end, today time.Time) int {
	count := 0
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if !d.Before(today) {
			continue
		}
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

func salesChartPoints(sales []DailySales, averageCents int64, start, end time.Time) []SalesChartPoint {
	if len(sales) == 0 {
		return nil
	}
	salesByDate := map[string]DailySales{}
	for _, sale := range sales {
		salesByDate[sale.BusinessDate] = sale
	}
	labelEvery := productivityChartLabelEvery(start, end)
	points := []SalesChartPoint{}
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		date := d.Format("2006-01-02")
		sale, ok := salesByDate[date]
		if !ok {
			continue
		}
		label := ""
		if len(points)%labelEvery == 0 || d.Equal(start) || d.Equal(end) {
			label = d.Format("01/02")
		}
		points = append(points, SalesChartPoint{
			Date:    date,
			Label:   label,
			Actual:  centsFloat(sale.TotalCents),
			Average: centsFloat(averageCents),
			Gap:     centsFloat(sale.TotalCents - averageCents),
		})
	}
	return points
}

func salesBenchmark(sales []DailySales) SalesBenchmark {
	values := make([]int64, 0, len(sales))
	for _, sale := range sales {
		if sale.TotalCents > 0 {
			values = append(values, sale.TotalCents)
		}
	}
	if len(values) == 0 {
		return SalesBenchmark{ExcludedDays: len(sales)}
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	median := values[len(values)/2]
	if len(values)%2 == 0 {
		median = (values[len(values)/2-1] + values[len(values)/2]) / 2
	}
	lowAnomalyLimit := median / 4
	var total int64
	var included int
	benchmark := SalesBenchmark{}
	for _, sale := range sales {
		if sale.TotalCents <= 0 || sale.TotalCents < lowAnomalyLimit {
			benchmark.ExcludedDays++
			benchmark.ExcludedDates = append(benchmark.ExcludedDates, sale.BusinessDate)
			continue
		}
		total += sale.TotalCents
		included++
	}
	benchmark.IncludedDays = included
	if included > 0 {
		benchmark.AverageCents = int64(math.Round(float64(total) / float64(included)))
	}
	return benchmark
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

type ProductivityDateRange struct {
	Start   time.Time
	End     time.Time
	IsRange bool
}

func productivityReport(db *sql.DB, locationID int64, month time.Time, today time.Time) (ProductivityReport, error) {
	first := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.Local)
	end := first.AddDate(0, 1, -1)
	if yesterday := today.AddDate(0, 0, -1); end.After(yesterday) {
		end = yesterday
	}
	return productivityReportForRange(db, locationID, ProductivityDateRange{Start: first, End: end}, today)
}

func productivityReportForRange(db *sql.DB, locationID int64, dateRange ProductivityDateRange, today time.Time) (ProductivityReport, error) {
	first := startOfDay(dateRange.Start)
	end := startOfDay(dateRange.End)
	displayEnd := end
	if displayEnd.Before(first) {
		displayEnd = first
	}
	rangeLabel := first.Format("January 2006")
	if dateRange.IsRange {
		rangeLabel = productivityRangeLabel(first, displayEnd)
	}
	report := ProductivityReport{
		LocationID:        locationID,
		Month:             first.Format("2006-01"),
		MonthLabel:        first.Format("January 2006"),
		PrevMonth:         first.AddDate(0, -1, 0).Format("2006-01"),
		NextMonth:         first.AddDate(0, 1, 0).Format("2006-01"),
		StartDate:         first.Format("2006-01-02"),
		EndDate:           displayEnd.Format("2006-01-02"),
		RangeLabel:        rangeLabel,
		IsRange:           dateRange.IsRange,
		MissingDates:      []string{},
		MissingGoalMonths: []string{},
	}
	goalEnd := end
	if goalEnd.Before(first) {
		goalEnd = first
	}
	goals, err := listMonthlyProductivityGoals(db, locationID, first.Format("2006-01"), goalEnd.Format("2006-01"))
	if err != nil {
		return ProductivityReport{}, err
	}
	if goal, ok := goals[report.Month]; ok {
		report.GoalBasisPoints = goal.GoalBasisPoints
		report.GoalDisplayValue = goal.GoalDisplayValue
	}
	if end.Before(first) {
		return report, nil
	}
	sales, err := listDailySales(db, locationID, first.Format("2006-01-02"), end.Format("2006-01-02"))
	if err != nil {
		return ProductivityReport{}, err
	}
	labor, err := listDailyLabor(db, locationID, first.Format("2006-01-02"), end.Format("2006-01-02"))
	if err != nil {
		return ProductivityReport{}, err
	}
	salesByDate := map[string]DailySales{}
	for _, sale := range sales {
		salesByDate[sale.BusinessDate] = sale
	}
	laborByDate := map[string]DailyLabor{}
	for _, day := range labor {
		laborByDate[day.BusinessDate] = day
	}
	missingGoalMonths := map[string]bool{}
	labelEvery := productivityChartLabelEvery(first, end)
	for d := first; !d.After(end); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Sunday {
			continue
		}
		date := d.Format("2006-01-02")
		month := d.Format("2006-01")
		goal, hasGoal := goals[month]
		if !hasGoal {
			missingGoalMonths[month] = true
			continue
		}
		sale, hasSales := salesByDate[date]
		day, hasLabor := laborByDate[date]
		if !hasSales || !hasLabor || day.TotalMinutes <= 0 {
			report.MissingDates = append(report.MissingDates, date)
			continue
		}
		productivity := productivityBasisPoints(sale.TotalCents, day.TotalMinutes)
		gap := productivity - goal.GoalBasisPoints
		label := ""
		if len(report.Chart)%labelEvery == 0 || d.Equal(first) || d.Equal(end) {
			label = d.Format("01/02")
		}
		row := ProductivityRow{
			Date:                    date,
			DateLabel:               formatISODate(date),
			Weekday:                 d.Format("Monday"),
			SalesCents:              sale.TotalCents,
			LaborMinutes:            day.TotalMinutes,
			LaborHours:              formatHours(day.TotalMinutes),
			ProductivityBasisPoints: productivity,
			ProductivityDisplay:     formatProductivityGoal(productivity),
			TargetBasisPoints:       goal.GoalBasisPoints,
			TargetDisplay:           goal.GoalDisplayValue,
			GapBasisPoints:          gap,
			GapDisplay:              formatProductivityGap(gap),
		}
		report.Rows = append(report.Rows, row)
		report.Chart = append(report.Chart, ProductivityChartPoint{
			Date:   date,
			Label:  label,
			Actual: basisPointsFloat(productivity),
			Target: basisPointsFloat(goal.GoalBasisPoints),
			Gap:    basisPointsFloat(gap),
		})
		report.TotalSalesCents += sale.TotalCents
		report.TotalLaborMinutes += day.TotalMinutes
	}
	for month := range missingGoalMonths {
		report.MissingGoalMonths = append(report.MissingGoalMonths, month)
	}
	sort.Strings(report.MissingGoalMonths)
	report.AverageBasisPoints = productivityBasisPoints(report.TotalSalesCents, report.TotalLaborMinutes)
	return report, nil
}

func productivityChartLabelEvery(start, end time.Time) int {
	days := int(end.Sub(start).Hours()/24) + 1
	switch {
	case days > 180:
		return 14
	case days > 90:
		return 7
	case days > 45:
		return 3
	default:
		return 1
	}
}

func productivityRangeLabel(start, end time.Time) string {
	if start.Format("2006-01-02") == end.Format("2006-01-02") {
		return formatISODate(start.Format("2006-01-02"))
	}
	return fmt.Sprintf("%s to %s", formatISODate(start.Format("2006-01-02")), formatISODate(end.Format("2006-01-02")))
}

func productivityBasisPoints(salesCents int64, laborMinutes int) int64 {
	if laborMinutes <= 0 {
		return 0
	}
	return int64(math.Round(float64(salesCents) * 60 / float64(laborMinutes)))
}

func basisPointsFloat(value int64) float64 {
	return float64(value) / 100
}

func centsFloat(value int64) float64 {
	return float64(value) / 100
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
	return start, end, nil
}

func missingLaborDates(start, end time.Time, labor []DailyLabor) []string {
	return missingLaborDatesBefore(start, end, labor, startOfDay(time.Now()))
}

func missingLaborDatesBefore(start, end time.Time, labor []DailyLabor, today time.Time) []string {
	present := map[string]bool{}
	for _, day := range labor {
		present[day.BusinessDate] = true
	}
	var missing []string
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if !d.Before(today) {
			continue
		}
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

func monthValueFromString(value string) (string, error) {
	month, err := time.ParseInLocation("2006-01", strings.TrimSpace(value), time.Local)
	if err != nil {
		return "", errors.New("month must use YYYY-MM format")
	}
	return month.Format("2006-01"), nil
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

func productivityRangeFromRequest(r *http.Request, today time.Time) (ProductivityDateRange, error) {
	startValue := strings.TrimSpace(r.URL.Query().Get("start_date"))
	endValue := strings.TrimSpace(r.URL.Query().Get("end_date"))
	if startValue == "" && endValue == "" {
		month, err := calendarMonthFromRequest(r)
		if err != nil {
			return ProductivityDateRange{}, err
		}
		end := month.AddDate(0, 1, -1)
		if yesterday := today.AddDate(0, 0, -1); end.After(yesterday) {
			end = yesterday
		}
		return ProductivityDateRange{Start: month, End: end}, nil
	}
	if startValue == "" || endValue == "" {
		return ProductivityDateRange{}, errors.New("start date and end date are required for productivity ranges")
	}
	start, err := time.ParseInLocation("2006-01-02", startValue, time.Local)
	if err != nil {
		return ProductivityDateRange{}, errors.New("start date must use YYYY-MM-DD format")
	}
	end, err := time.ParseInLocation("2006-01-02", endValue, time.Local)
	if err != nil {
		return ProductivityDateRange{}, errors.New("end date must use YYYY-MM-DD format")
	}
	if end.Before(start) {
		return ProductivityDateRange{}, errors.New("end date must be on or after start date")
	}
	if int(end.Sub(start).Hours()/24)+1 > 365 {
		return ProductivityDateRange{}, errors.New("productivity ranges can include at most 365 days")
	}
	yesterday := today.AddDate(0, 0, -1)
	if start.After(yesterday) {
		return ProductivityDateRange{}, errors.New("productivity ranges cannot start after yesterday")
	}
	if end.After(yesterday) {
		end = yesterday
	}
	return ProductivityDateRange{Start: start, End: end, IsRange: true}, nil
}

func parseProductivityGoal(value string) (int64, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, ",", ""))
	if value == "" {
		return 0, errors.New("productivity goal is required")
	}
	if strings.HasPrefix(value, "-") {
		return 0, errors.New("productivity goal cannot be negative")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, errors.New("productivity goal must be a number")
	}
	for _, r := range parts[0] {
		if r < '0' || r > '9' {
			return 0, errors.New("productivity goal must be a number")
		}
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, errors.New("productivity goal must be a number")
	}
	var fraction int64
	if len(parts) == 2 {
		if len(parts[1]) > 2 {
			return 0, errors.New("productivity goal can have at most two decimal places")
		}
		for len(parts[1]) < 2 {
			parts[1] += "0"
		}
		for _, r := range parts[1] {
			if r < '0' || r > '9' {
				return 0, errors.New("productivity goal must be a number")
			}
		}
		fraction, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return 0, errors.New("productivity goal must be a number")
		}
	}
	if whole > 1000000 {
		return 0, errors.New("productivity goal is too large")
	}
	return whole*100 + fraction, nil
}

func formatProductivityGoal(value int64) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	whole := value / 100
	fraction := value % 100
	if fraction == 0 {
		return sign + strconv.FormatInt(whole, 10)
	}
	out := fmt.Sprintf("%d.%02d", whole, fraction)
	return sign + strings.TrimRight(out, "0")
}

func formatProductivityGap(value int64) string {
	sign := ""
	if value > 0 {
		sign = "+"
	}
	return sign + formatProductivityGoal(value)
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

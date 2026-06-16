package main

import (
	"database/sql"
	"time"
)

const (
	appName        = "cfasuite-hr"
	defaultPort    = "8217"
	defaultDBPath  = "data/cfasuite-hr.db"
	sessionCookie  = "cfasuite_hr_session"
	sessionTTL     = 12 * time.Hour
	banWindow      = 24 * time.Hour
	maxLoginFails  = 5
	maxUploadBytes = 20 << 20
)

var requiredColumns = []string{
	"Employee Name",
	"Employee Number",
	"Job",
	"Employee Status",
	"Location Latest Start Date",
}

var requiredBirthdayColumns = []string{
	"Employee Name",
	"Birth Date",
}

type App struct {
	db            *sql.DB
	sessionSecret []byte
	adminUsername string
	adminPassword string
	adminHash     string
}

type Location struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Number    string    `json:"number"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Employees int       `json:"employee_count,omitempty"`
}

type Employee struct {
	ID                      int64     `json:"id"`
	LocationID              int64     `json:"location_id"`
	EmployeeName            string    `json:"employee_name"`
	EmployeeNumber          string    `json:"employee_number"`
	Job                     string    `json:"job"`
	RoleID                  *int64    `json:"role_id"`
	RoleName                *string   `json:"role_name"`
	DepartmentID            *int64    `json:"department_id"`
	DepartmentName          *string   `json:"department_name"`
	WageRateCents           *int64    `json:"wage_rate_cents"`
	WagePayType             string    `json:"wage_pay_type"`
	ExcludeFromLabor        bool      `json:"exclude_from_labor"`
	EmployeeStatus          string    `json:"employee_status"`
	LocationLatestStartDate string    `json:"location_latest_start_date"`
	BirthDate               *string   `json:"birth_date"`
	ClockInPIN              *string   `json:"clock_in_pin"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

type AssignmentStatus struct {
	RoleUnassigned       int
	DepartmentUnassigned int
}

type CalendarDay struct {
	Date          string
	Label         string
	Day           int
	CurrentMonth  bool
	Today         bool
	HasSales      bool
	HasLabor      bool
	SalesRequired bool
	LaborRequired bool
	Complete      bool
	Sunday        bool
	Accessible    bool
}

type Role struct {
	ID         int64     `json:"id"`
	LocationID int64     `json:"location_id"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Employees  int       `json:"employee_count,omitempty"`
}

type Department struct {
	ID         int64     `json:"id"`
	LocationID int64     `json:"location_id"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Employees  int       `json:"employee_count,omitempty"`
}

type Token struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Prefix     string    `json:"prefix"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt *string   `json:"last_used_at,omitempty"`
}

type ImportResult struct {
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Removed int `json:"removed"`
	Skipped int `json:"skipped"`
}

type BioEmployee struct {
	Name            string
	Number          string
	Job             string
	Status          string
	LatestStartDate string
}

type BirthdayEmployee struct {
	Name      string
	BirthDate string
}

type PinEmployee struct {
	Name       string
	ClockInPIN string
}

type DaypartSalesReport struct {
	StoreName    string
	BusinessDate string
	Dayparts     map[string]int64
	Destinations map[string]int64
}

type DailySales struct {
	LocationID   int64
	BusinessDate string
	TotalCents   int64
	Dayparts     map[string]int64
	Destinations map[string]int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type SalesBreakdownRow struct {
	Label   string
	Cents   int64
	Percent string
}

type SalesDailyRow struct {
	Date         string
	DateLabel    string
	Weekday      string
	TotalCents   int64
	Dayparts     []SalesBreakdownRow
	Destinations []SalesBreakdownRow
}

type TimePunchReport struct {
	Title        string
	LocationName string
	PeriodLabel  string
	StartDate    string
	EndDate      string
	Employees    []LaborEmployee
	GrandTotals  LaborTotals
}

type LaborEmployee struct {
	Name             string
	Job              string
	Role             string
	Department       string
	EmployeeNumber   string
	WageRateCents    int64
	WagePayType      string
	ExcludeFromLabor bool
	Days             []LaborDay
	Totals           LaborTotals
}

type LaborDay struct {
	Weekday            string
	Date               string
	Minutes            int
	OvertimeMinutes    int
	WagesCents         int64
	OvertimeWagesCents int64
}

type LaborTotals struct {
	Minutes            int
	OvertimeMinutes    int
	WagesCents         int64
	OvertimeWagesCents int64
}

type LaborSummary struct {
	Label   string
	Hours   string
	Dollars string
}

type LaborEmployeeRow struct {
	Name            string
	Job             string
	Role            string
	Department      string
	Hours           string
	Dollars         string
	OvertimeDollars string
	Percent         string
	MinutesValue    int
	CentsValue      int64
}

type LaborDayRow struct {
	Day           string
	Date          string
	Hours         string
	OvertimeHours string
	Dollars       string
	Percent       string
}

type DailyLabor struct {
	LocationID      int64
	BusinessDate    string
	TotalMinutes    int
	OvertimeMinutes int
	TotalWagesCents int64
	Roles           map[string]LaborTotals
	Departments     map[string]LaborTotals
	Jobs            map[string]LaborTotals
	Employees       map[string]LaborTotals
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type LaborDailyRow struct {
	Date          string
	DateLabel     string
	Weekday       string
	Hours         string
	OvertimeHours string
	Dollars       string
	MinutesValue  int
	CentsValue    int64
}

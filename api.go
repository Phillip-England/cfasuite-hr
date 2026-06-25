package main

import "net/http"

func (a *App) apiLocations(w http.ResponseWriter, r *http.Request) {
	locations, err := listLocations(a.db)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"locations": locations})
}

func (a *App) apiEmployees(w http.ResponseWriter, r *http.Request) {
	loc, err := getLocationByNumber(a.db, r.PathValue("number"))
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "location not found")
		return
	}
	employees, err := listEmployees(a.db, loc.ID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"location": loc, "employees": employees})
}

func (a *App) apiEmployeeIdentities(w http.ResponseWriter, r *http.Request) {
	loc, err := getLocationByNumber(a.db, r.PathValue("number"))
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "location not found")
		return
	}
	employees, err := listEmployees(a.db, loc.ID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"location": loc, "employees": employeeIdentities(employees)})
}

func (a *App) apiEmployee(w http.ResponseWriter, r *http.Request) {
	loc, err := getLocationByNumber(a.db, r.PathValue("number"))
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "location not found")
		return
	}
	employee, err := getEmployee(a.db, loc.ID, r.PathValue("employeeNumber"))
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "employee not found")
		return
	}
	writeJSON(w, map[string]any{"location": loc, "employee": employee})
}

func (a *App) apiEmployeeIdentity(w http.ResponseWriter, r *http.Request) {
	loc, err := getLocationByNumber(a.db, r.PathValue("number"))
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "location not found")
		return
	}
	employee, err := getEmployee(a.db, loc.ID, r.PathValue("employeeNumber"))
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "employee not found")
		return
	}
	writeJSON(w, map[string]any{"location": loc, "employee": employeeIdentity(employee)})
}

func employeeIdentities(employees []Employee) []EmployeeIdentity {
	identities := make([]EmployeeIdentity, 0, len(employees))
	for _, employee := range employees {
		identities = append(identities, employeeIdentity(employee))
	}
	return identities
}

func employeeIdentity(employee Employee) EmployeeIdentity {
	return EmployeeIdentity{
		ID:             employee.ID,
		LocationID:     employee.LocationID,
		EmployeeName:   employee.EmployeeName,
		EmployeeNumber: employee.EmployeeNumber,
		Job:            employee.Job,
		RoleName:       employee.RoleName,
		DepartmentName: employee.DepartmentName,
		EmployeeStatus: employee.EmployeeStatus,
		BirthDate:      employee.BirthDate,
	}
}

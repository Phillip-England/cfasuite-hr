package main

const layoutHTML = `{{define "layout"}}<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}} | cfasuite-hr</title>
  <link rel="stylesheet" href="/assets/app.css">
</head>
<body>
  <header>
    <div class="brand-wrap">
      <a class="brand" href="/">cfasuite-hr</a>
      {{with .Location}}<span class="location-context">Store {{.Number}}</span>{{end}}
    </div>
    {{if not .LoggedOut}}<nav>
      <a href="/">Locations</a>
      <a href="/tokens">API Tokens</a>
      <a href="/docs">API Docs</a>
      <form method="post" action="/logout"><button class="ghost">Sign out</button></form>
    </nav>{{end}}
  </header>
  <main>{{template "body" .}}</main>
</body>
</html>{{end}}`

const loginHTML = `{{define "body"}}
<section class="narrow">
  <h1>Admin Login</h1>
  {{if .Error}}<p class="notice bad">{{.Error}}</p>{{end}}
  {{if not .Configured}}<p class="notice bad">Admin credentials are not configured. Set CFASUITE_ADMIN_USERNAME and CFASUITE_ADMIN_PASSWORD or run the set-admin CLI command.</p>{{end}}
  <form method="post" action="/login" class="panel">
    <label>Username <input name="username" autocomplete="username" required></label>
    <label>Password <input name="password" type="password" autocomplete="current-password" required></label>
    <button>Log in</button>
  </form>
</section>
{{end}}`

const dashboardHTML = `{{define "body"}}
<div class="row">
  <h1>Locations</h1>
  <a class="button" href="/locations/new">New location</a>
</div>
<section class="grid">
{{range .Locations}}
  <article class="card">
    <h2>{{.Name}}</h2>
    <p class="muted">Store {{.Number}}</p>
    <p>{{.Employees}} active employees</p>
    <a class="button secondary" href="/locations/{{.ID}}">Manage</a>
  </article>
{{else}}
  <p class="empty">No locations yet.</p>
{{end}}
</section>
{{end}}`

const locationFormHTML = `{{define "body"}}
<section class="narrow">
  <h1>{{.Mode}} Location</h1>
  <form method="post" action="{{.Action}}" class="panel">
    <label>Name <input name="name" value="{{.Location.Name}}" required></label>
    <label>Store number <input name="number" value="{{.Location.Number}}" required></label>
    <label>Store email <input type="email" name="email" value="{{.Location.Email}}" required></label>
    <button>{{.Mode}}</button>
  </form>
</section>
{{end}}`

const locationShowHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>{{.Location.Name}}</h1>
    <p class="muted">Store {{.Location.Number}}</p>
  </div>
</div>
<nav class="portal-menu">
  <a class="active" href="/locations/{{.Location.ID}}">Overview</a>
  <a href="/locations/{{.Location.ID}}/details">Employee Details</a>
  <a href="/locations/{{.Location.ID}}/pay">Employee Pay</a>
  <a href="/locations/{{.Location.ID}}/calendar">Calendar</a>
  <a href="/locations/{{.Location.ID}}/sales">Sales</a>
  <a href="/locations/{{.Location.ID}}/documents">Documents</a>
  <a href="/locations/{{.Location.ID}}/edit">Edit</a>
  <a href="/locations/{{.Location.ID}}/labor">Labor</a>
  <a href="/locations/{{.Location.ID}}/departments">Departments</a>
  <a href="/locations/{{.Location.ID}}/roles">Roles</a>
</nav>
<section class="overview-grid">
  <article class="metric">
    <span>Store number</span>
    <strong>{{.Location.Number}}</strong>
  </article>
  <article class="metric">
    <span>Active employees</span>
    <strong>{{len .Employees}}</strong>
  </article>
  <article class="metric">
    <span>Created</span>
    <strong>{{.Location.CreatedAt.Format "2006-01-02"}}</strong>
  </article>
  <article class="metric">
    <span>Updated</span>
    <strong>{{.Location.UpdatedAt.Format "2006-01-02"}}</strong>
  </article>
</section>
<section>
  <div class="section-head compact">
    <h2>Employee overview</h2>
    <div class="assignment-status" aria-label="Assignment status">
      <span><strong id="role-unassigned-count">{{.AssignmentStatus.RoleUnassigned}}</strong> no role</span>
      <span><strong id="department-unassigned-count">{{.AssignmentStatus.DepartmentUnassigned}}</strong> no department</span>
    </div>
  </div>
  {{if .Import.Get "roles_assigned"}}<p class="notice">Updated roles for {{.Import.Get "roles_assigned"}} employees.</p>{{end}}
  {{if .Import.Get "departments_assigned"}}<p class="notice">Updated departments for {{.Import.Get "departments_assigned"}} employees.</p>{{end}}
  <div class="employee-filters">
    <label>Job
      <select id="employee-job-filter">
        <option value="">All jobs</option>
        {{range .JobOptions}}<option value="{{.}}">{{.}}</option>{{end}}
      </select>
    </label>
    <label>Role
      <select id="employee-role-filter">
        <option value="">All roles</option>
        <option value="__assigned">Assigned role</option>
        <option value="__unassigned">Unassigned role</option>
        {{range .Roles}}<option value="{{.ID}}">{{.Name}}</option>{{end}}
      </select>
    </label>
    <label>Department
      <select id="employee-department-filter">
        <option value="">All departments</option>
        <option value="__assigned">Assigned department</option>
        <option value="__unassigned">Unassigned department</option>
        {{range .Departments}}<option value="{{.ID}}">{{.Name}}</option>{{end}}
      </select>
    </label>
  </div>
  <table>
    <thead><tr><th>Name</th><th>Employee #</th><th>Job</th><th>Role</th><th>Department</th></tr></thead>
    <tbody id="employee-rows">
    {{range .Employees}}{{$employee := .}}<tr data-job="{{.Job}}" data-role="{{if .RoleID}}{{.RoleID}}{{else}}__unassigned{{end}}" data-department="{{if .DepartmentID}}{{.DepartmentID}}{{else}}__unassigned{{end}}"><td><a class="employee-name-link" href="/locations/{{$.Location.ID}}/employees/{{.ID}}"><span>{{.EmployeeName}}</span></a></td><td>{{.EmployeeNumber}}</td><td>{{.Job}}</td><td><form method="post" action="/locations/{{$.Location.ID}}/assignments" class="assignment-form"><input type="hidden" name="assignment" value="role"><input type="hidden" name="employee_id" value="{{.ID}}"><select name="role_id" aria-label="Role for {{.EmployeeName}}"><option value="" {{if not .RoleID}}selected{{end}}>Unassigned</option>{{range $.Roles}}<option value="{{.ID}}" {{if selectedID $employee.RoleID .ID}}selected{{end}}>{{.Name}}</option>{{end}}</select></form></td><td><form method="post" action="/locations/{{$.Location.ID}}/assignments" class="assignment-form"><input type="hidden" name="assignment" value="department"><input type="hidden" name="employee_id" value="{{.ID}}"><select name="department_id" aria-label="Department for {{.EmployeeName}}"><option value="" {{if not .DepartmentID}}selected{{end}}>Unassigned</option>{{range $.Departments}}<option value="{{.ID}}" {{if selectedID $employee.DepartmentID .ID}}selected{{end}}>{{.Name}}</option>{{end}}</select></form></td></tr>{{else}}<tr><td colspan="5">No employees imported.</td></tr>{{end}}
    <tr id="employee-filter-empty" hidden><td colspan="5">No employees match these filters.</td></tr>
    </tbody>
  </table>
</section>
<script>
(() => {
  const rows = Array.from(document.querySelectorAll('#employee-rows tr[data-job]'));
  const empty = document.getElementById('employee-filter-empty');
  const job = document.getElementById('employee-job-filter');
  const role = document.getElementById('employee-role-filter');
  const department = document.getElementById('employee-department-filter');
  const assignmentForms = Array.from(document.querySelectorAll('.assignment-form'));
  const roleUnassigned = document.getElementById('role-unassigned-count');
  const departmentUnassigned = document.getElementById('department-unassigned-count');
  function matchesAssignment(value, current) {
    if (!value) return true;
    if (value === '__assigned') return current !== '__unassigned';
    return current === value;
  }
  function visibleRows() {
    return rows.filter(row => !row.hidden);
  }
  function applyFilters() {
    rows.forEach(row => {
      const hidden = Boolean(job.value && row.dataset.job !== job.value) ||
        !matchesAssignment(role.value, row.dataset.role) ||
        !matchesAssignment(department.value, row.dataset.department);
      row.hidden = hidden;
    });
    if (empty) empty.hidden = visibleRows().length !== 0;
  }
  function updateAssignmentStatus() {
    if (roleUnassigned) roleUnassigned.textContent = rows.filter(row => row.dataset.role === '__unassigned').length;
    if (departmentUnassigned) departmentUnassigned.textContent = rows.filter(row => row.dataset.department === '__unassigned').length;
  }
  [job, role, department].forEach(control => control.addEventListener('input', applyFilters));
  assignmentForms.forEach(form => {
    const control = form.querySelector('select, input[type="checkbox"]');
    if (!control) return;
    control.dataset.previousValue = control.type === 'checkbox' ? String(control.checked) : control.value;
    control.addEventListener('change', async () => {
      const row = form.closest('tr');
      const assignment = form.querySelector('input[name="assignment"]').value;
      const previousValue = control.dataset.previousValue || '';
      const data = new FormData(form);
      control.disabled = true;
      try {
        const response = await fetch(form.action, {
          method: 'POST',
          body: data,
          headers: {'X-Requested-With': 'fetch'},
        });
        if (!response.ok) throw new Error(await response.text());
        control.dataset.previousValue = control.type === 'checkbox' ? String(control.checked) : control.value;
        if (assignment === 'role' || assignment === 'department') {
          row.dataset[assignment] = control.value || '__unassigned';
          updateAssignmentStatus();
          applyFilters();
        }
      } catch (error) {
        if (control.type === 'checkbox') {
          control.checked = previousValue === 'true';
        } else {
          control.value = previousValue;
        }
        alert((error.message || 'Unable to update assignment').trim());
      } finally {
        control.disabled = false;
      }
    });
  });
  updateAssignmentStatus();
  applyFilters();
})();
</script>
{{end}}`

const locationDetailsHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>{{.Location.Name}} Employee Details</h1>
    <p class="muted">Store {{.Location.Number}}</p>
  </div>
</div>
<nav class="portal-menu">
  <a href="/locations/{{.Location.ID}}">Overview</a>
  <a class="active" href="/locations/{{.Location.ID}}/details">Employee Details</a>
  <a href="/locations/{{.Location.ID}}/pay">Employee Pay</a>
  <a href="/locations/{{.Location.ID}}/calendar">Calendar</a>
  <a href="/locations/{{.Location.ID}}/sales">Sales</a>
  <a href="/locations/{{.Location.ID}}/documents">Documents</a>
  <a href="/locations/{{.Location.ID}}/edit">Edit</a>
  <a href="/locations/{{.Location.ID}}/labor">Labor</a>
  <a href="/locations/{{.Location.ID}}/departments">Departments</a>
  <a href="/locations/{{.Location.ID}}/roles">Roles</a>
</nav>
<section class="overview-grid">
  <article class="metric">
    <span>Store number</span>
    <strong>{{.Location.Number}}</strong>
  </article>
  <article class="metric">
    <span>Active employees</span>
    <strong>{{len .Employees}}</strong>
  </article>
  <article class="metric">
    <span>Created</span>
    <strong>{{.Location.CreatedAt.Format "2006-01-02"}}</strong>
  </article>
  <article class="metric">
    <span>Updated</span>
    <strong>{{.Location.UpdatedAt.Format "2006-01-02"}}</strong>
  </article>
</section>
<section>
  <div class="section-head">
    <h2>Employee details</h2>
    <label class="search-control">Name
      <input id="employee-detail-search" type="search" placeholder="Filter by name">
    </label>
  </div>
  <table>
    <thead><tr><th>Name</th><th>Start date</th><th>Birthday</th><th>Clock-in PIN</th></tr></thead>
    <tbody id="employee-detail-rows">
    {{range .Employees}}<tr data-name="{{.EmployeeName}}"><td><a class="employee-name-link" href="/locations/{{$.Location.ID}}/employees/{{.ID}}"><span>{{.EmployeeName}}</span></a></td><td>{{.LocationLatestStartDate}}</td><td>{{if .BirthDate}}{{.BirthDate}}{{else}}<span class="muted">Unknown</span>{{end}}</td><td>{{if .ClockInPIN}}{{.ClockInPIN}}{{else}}<span class="muted">Not imported</span>{{end}}</td></tr>{{else}}<tr><td colspan="4">No employees imported.</td></tr>{{end}}
    <tr id="employee-detail-empty" hidden><td colspan="4">No employees match this filter.</td></tr>
    </tbody>
  </table>
</section>
<script>
(() => {
  const rows = Array.from(document.querySelectorAll('#employee-detail-rows tr[data-name]'));
  const empty = document.getElementById('employee-detail-empty');
  const search = document.getElementById('employee-detail-search');
  if (search) {
    search.addEventListener('input', () => {
      const value = search.value.trim().toLowerCase();
      rows.forEach(row => row.hidden = value && !(row.dataset.name || '').toLowerCase().includes(value));
      if (empty) empty.hidden = rows.some(row => !row.hidden);
    });
  }
})();
</script>
{{end}}`

const locationPayHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>{{.Location.Name}} Employee Pay</h1>
    <p class="muted">Store {{.Location.Number}}</p>
  </div>
</div>
<nav class="portal-menu">
  <a href="/locations/{{.Location.ID}}">Overview</a>
  <a href="/locations/{{.Location.ID}}/details">Employee Details</a>
  <a class="active" href="/locations/{{.Location.ID}}/pay">Employee Pay</a>
  <a href="/locations/{{.Location.ID}}/calendar">Calendar</a>
  <a href="/locations/{{.Location.ID}}/sales">Sales</a>
  <a href="/locations/{{.Location.ID}}/documents">Documents</a>
  <a href="/locations/{{.Location.ID}}/edit">Edit</a>
  <a href="/locations/{{.Location.ID}}/labor">Labor</a>
  <a href="/locations/{{.Location.ID}}/departments">Departments</a>
  <a href="/locations/{{.Location.ID}}/roles">Roles</a>
</nav>
{{if .Import.Get "wages_assigned"}}<p class="notice">Updated wages for {{.Import.Get "wages_assigned"}} employee records.</p>{{end}}
{{if .Import.Get "labor_exclusions"}}<p class="notice">Updated labor exclusion for {{.Import.Get "labor_exclusions"}} employees.</p>{{end}}
<section>
  <div class="section-head">
    <h2>Employee pay</h2>
    <label class="search-control">Name
      <input id="employee-pay-search" type="search" placeholder="Filter by name">
    </label>
  </div>
  <table>
    <thead><tr><th>Name</th><th>Pay type</th><th>Wage</th><th>Exclude labor</th></tr></thead>
    <tbody id="employee-pay-rows">
    {{range .Employees}}<tr data-name="{{.EmployeeName}}"><td><a class="employee-name-link" href="/locations/{{$.Location.ID}}/employees/{{.ID}}"><span>{{.EmployeeName}}</span></a></td><td><form method="post" action="/locations/{{$.Location.ID}}/assignments" class="wage-form"><input type="hidden" name="assignment" value="wage"><input type="hidden" name="employee_id" value="{{.ID}}"><input type="hidden" name="wage_rate" value="{{formatWageInput .WageRateCents}}"><select name="wage_pay_type" aria-label="Pay type for {{.EmployeeName}}"><option value="" {{if eq .WagePayType ""}}selected{{end}}>Unknown</option><option value="hourly" {{if eq .WagePayType "hourly"}}selected{{end}}>Hourly</option><option value="salary" {{if eq .WagePayType "salary"}}selected{{end}}>Salary</option></select></form></td><td><form method="post" action="/locations/{{$.Location.ID}}/assignments" class="wage-form"><input type="hidden" name="assignment" value="wage"><input type="hidden" name="employee_id" value="{{.ID}}"><input type="hidden" name="wage_pay_type" value="{{.WagePayType}}"><input name="wage_rate" inputmode="decimal" value="{{formatWageInput .WageRateCents}}" placeholder="0.00" aria-label="Wage for {{.EmployeeName}}"></form></td><td><form method="post" action="/locations/{{$.Location.ID}}/assignments" class="assignment-form labor-exclusion-form"><input type="hidden" name="assignment" value="labor_exclusion"><input type="hidden" name="employee_id" value="{{.ID}}"><input type="hidden" name="exclude_from_labor" value="0"><input type="checkbox" name="exclude_from_labor" value="1" aria-label="Exclude {{.EmployeeName}} from labor calculations" {{if .ExcludeFromLabor}}checked{{end}}></form></td></tr>{{else}}<tr><td colspan="4">No employees imported.</td></tr>{{end}}
    <tr id="employee-pay-empty" hidden><td colspan="4">No employees match this filter.</td></tr>
    </tbody>
  </table>
</section>
<script>
(() => {
  const rows = Array.from(document.querySelectorAll('#employee-pay-rows tr[data-name]'));
  const empty = document.getElementById('employee-pay-empty');
  const search = document.getElementById('employee-pay-search');
  if (search) {
    search.addEventListener('input', () => {
      const value = search.value.trim().toLowerCase();
      rows.forEach(row => row.hidden = value && !(row.dataset.name || '').toLowerCase().includes(value));
      if (empty) empty.hidden = rows.some(row => !row.hidden);
    });
  }
  async function submitForm(form) {
    const controls = Array.from(form.querySelectorAll('input, select'));
    const previous = controls.map(control => control.type === 'checkbox' ? control.checked : control.value);
    const data = new FormData(form);
    controls.forEach(control => control.disabled = true);
    try {
      const response = await fetch(form.action, {
        method: 'POST',
        body: data,
        headers: {'X-Requested-With': 'fetch'},
      });
      if (!response.ok) throw new Error(await response.text());
    } catch (error) {
      controls.forEach((control, index) => {
        if (control.type === 'checkbox') control.checked = previous[index];
        else control.value = previous[index];
      });
      alert((error.message || 'Unable to update employee details').trim());
    } finally {
      controls.forEach(control => control.disabled = false);
    }
  }
  document.querySelectorAll('.labor-exclusion-form input[type="checkbox"]').forEach(control => {
    control.addEventListener('change', () => submitForm(control.form));
  });
  document.querySelectorAll('.wage-form select').forEach(control => {
    control.addEventListener('change', () => {
      const row = control.closest('tr');
      const wageInput = row ? row.querySelector('input[name="wage_rate"]:not([type="hidden"])') : null;
      const ownHiddenWage = control.form.querySelector('input[name="wage_rate"]');
      if (wageInput && ownHiddenWage) ownHiddenWage.value = wageInput.value;
      if (wageInput) {
        const hiddenPayType = wageInput.form.querySelector('input[name="wage_pay_type"]');
        if (hiddenPayType) hiddenPayType.value = control.value;
      }
      submitForm(control.form);
    });
  });
  document.querySelectorAll('.wage-form input[name="wage_rate"]').forEach(control => {
    if (control.type === 'hidden') return;
    control.addEventListener('change', () => {
      const row = control.closest('tr');
      const selectFormHiddenWage = row ? row.querySelector('select[name="wage_pay_type"]')?.form.querySelector('input[name="wage_rate"]') : null;
      if (selectFormHiddenWage) selectFormHiddenWage.value = control.value;
      submitForm(control.form);
    });
    control.addEventListener('keydown', event => {
      if (event.key === 'Enter') {
        event.preventDefault();
        control.blur();
      }
    });
  });
})();
</script>
{{end}}`

const locationCalendarHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>{{.Location.Name}} Calendar</h1>
    <p class="muted">Store {{.Location.Number}}</p>
  </div>
</div>
<nav class="portal-menu">
  <a href="/locations/{{.Location.ID}}">Overview</a>
  <a href="/locations/{{.Location.ID}}/details">Employee Details</a>
  <a href="/locations/{{.Location.ID}}/pay">Employee Pay</a>
  <a class="active" href="/locations/{{.Location.ID}}/calendar">Calendar</a>
  <a href="/locations/{{.Location.ID}}/sales">Sales</a>
  <a href="/locations/{{.Location.ID}}/productivity?month={{.MonthValue}}">Productivity</a>
  <a href="/locations/{{.Location.ID}}/documents">Documents</a>
  <a href="/locations/{{.Location.ID}}/edit">Edit</a>
  <a href="/locations/{{.Location.ID}}/labor">Labor</a>
  <a href="/locations/{{.Location.ID}}/departments">Departments</a>
  <a href="/locations/{{.Location.ID}}/roles">Roles</a>
</nav>
<section class="panel">
  <div class="section-head compact">
    <div>
      <h2>Sales upload calendar</h2>
      <p class="muted">Select a day to upload or review that day's sales report.</p>
    </div>
    <a class="button secondary" href="/locations/{{.Location.ID}}/sales">Generate sales report</a>
  </div>
  <div class="calendar-head">
    <a class="button secondary" href="/locations/{{.Location.ID}}/calendar?month={{.PrevMonth}}">Previous</a>
    <h2>{{.MonthLabel}}</h2>
    <a class="button secondary" href="/locations/{{.Location.ID}}/calendar?month={{.NextMonth}}">Next</a>
  </div>
  <form method="post" action="/locations/{{.Location.ID}}/calendar/productivity-goal" class="goal-form">
    <input type="hidden" name="month" value="{{.MonthValue}}">
    <label>Productivity goal for {{.MonthLabel}}
      <input type="number" name="productivity_goal" min="0" step="0.01" value="{{.ProductivityGoalValue}}" placeholder="Set goal">
    </label>
    <button type="submit">Save goal</button>
  </form>
  <div class="calendar-grid calendar-weekdays" aria-hidden="true">
    <span>Sun</span><span>Mon</span><span>Tue</span><span>Wed</span><span>Thu</span><span>Fri</span><span>Sat</span>
  </div>
  <div class="calendar-grid">
    {{range .Days}}{{if .Accessible}}<a class="calendar-day {{if not .CurrentMonth}}outside{{end}} {{if .Sunday}}sunday{{end}} {{if .Today}}today{{end}} {{if .Complete}}complete{{else if .SalesRequired}}missing-sales{{end}}" href="/locations/{{$.Location.ID}}/calendar/{{.Date}}" aria-label="{{.Label}}"><span>{{.Day}}</span><small>{{if .CurrentMonth}}{{if .Sunday}}Sunday{{else if .Complete}}Complete{{else if and .HasSales (not .HasLabor)}}Needs labor{{else if and .HasLabor (not .HasSales)}}Needs sales{{else}}Needs sales and labor{{end}}{{end}}</small></a>{{else}}<span class="calendar-day locked {{if not .CurrentMonth}}outside{{end}} {{if .Today}}today{{end}}" aria-label="{{.Label}}"><span>{{.Day}}</span><small>{{if .CurrentMonth}}{{if .Today}}Today{{else}}Future{{end}}{{end}}</small></span>{{end}}{{end}}
  </div>
</section>
{{end}}`

const locationCalendarDayHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>{{.Location.Name}} Calendar</h1>
    <p class="muted">Store {{.Location.Number}} | {{.DateLabel}}</p>
  </div>
  <div class="actions">
    <a class="button secondary" href="{{.PrevDayURL}}">Previous day</a>
    <a class="button" href="{{.BackToMonth}}">Back to calendar</a>
    {{if .NextDayURL}}<a class="button secondary" href="{{.NextDayURL}}">Next day</a>{{end}}
  </div>
</div>
<nav class="portal-menu">
  <a href="/locations/{{.Location.ID}}">Overview</a>
  <a href="/locations/{{.Location.ID}}/details">Employee Details</a>
  <a href="/locations/{{.Location.ID}}/pay">Employee Pay</a>
  <a class="active" href="/locations/{{.Location.ID}}/calendar?month={{.MonthValue}}">Calendar</a>
  <a href="/locations/{{.Location.ID}}/sales">Sales</a>
  <a href="/locations/{{.Location.ID}}/productivity?month={{.MonthValue}}">Productivity</a>
  <a href="/locations/{{.Location.ID}}/documents">Documents</a>
  <a href="/locations/{{.Location.ID}}/edit">Edit</a>
  <a href="/locations/{{.Location.ID}}/labor">Labor</a>
  <a href="/locations/{{.Location.ID}}/departments">Departments</a>
  <a href="/locations/{{.Location.ID}}/roles">Roles</a>
</nav>
<section class="panel day-panel">
  <div class="section-head compact">
    <div>
      <h2>{{.DateLabel}}</h2>
      <p class="muted">Sales, labor, and uploads for Store {{.Location.Number}}</p>
    </div>
  </div>
  {{if .Import.Get "sales_imported"}}<p class="notice">Daypart activity report imported.</p>{{end}}
  {{if .Import.Get "labor_imported"}}<p class="notice">Time punch labor imported.</p>{{end}}

  <div class="status-list">
    {{if not .Sales.BusinessDate}}<p class="notice bad">Sales data has not been uploaded for this date.</p>{{end}}
    {{if not .Labor.BusinessDate}}<p class="notice bad">Labor data has not been uploaded for this date.</p>{{end}}
  </div>

  {{if .Sales.BusinessDate}}
    <div class="data-block">
    <h3>Sales data</h3>
    <p><strong>{{formatMoney .Sales.TotalCents}}</strong> total sales</p>
    <section class="split">
      <div>
        <h2>Dayparts</h2>
        <table><tbody>{{range salesRowsForLabels .Sales.Dayparts salesDayparts}}<tr><td>{{.Label}}</td><td>{{formatMoney .Cents}}</td><td>{{.Percent}}</td></tr>{{end}}</tbody></table>
      </div>
      <div>
        <h2>Destinations</h2>
        <table><tbody>{{range salesRowsForLabels .Sales.Destinations salesDestinations}}<tr><td>{{.Label}}</td><td>{{formatMoney .Cents}}</td><td>{{.Percent}}</td></tr>{{end}}</tbody></table>
      </div>
    </section>
    </div>
  {{end}}

  {{if .Labor.BusinessDate}}
    <div class="data-block">
    <h3>Labor data</h3>
    <section class="overview-grid">
      <article class="metric"><span>Hours</span><strong>{{formatHours .Labor.TotalMinutes}}</strong></article>
      <article class="metric"><span>Overtime</span><strong>{{formatHours .Labor.OvertimeMinutes}}</strong></article>
      <article class="metric"><span>Labor dollars</span><strong>{{formatMoney .Labor.TotalWagesCents}}</strong></article>
      <article class="metric"><span>Date</span><strong>{{formatISODate .Labor.BusinessDate}}</strong></article>
    </section>
    </div>
  {{end}}

  <div class="upload-grid">
  <div>
    <h3>Upload sales data</h3>
    <form method="post" action="/locations/{{.Location.ID}}/calendar/{{.Date}}/sales" enctype="multipart/form-data" class="labor-upload">
      <label>Daypart activity PDF
        <input type="file" name="daypart_activity" accept=".pdf" required>
      </label>
      <button>Upload sales</button>
    </form>
  </div>
  <div>
    <h3>Upload labor data</h3>
    <form method="post" action="/locations/{{.Location.ID}}/calendar/{{.Date}}/labor" enctype="multipart/form-data" class="labor-upload">
      <label>Time punch PDF
        <input type="file" name="time_punch" accept=".pdf" required>
      </label>
      <button>Upload labor</button>
    </form>
  </div>
  </div>
</section>
{{end}}`

const locationSalesHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>{{.Location.Name}} Sales</h1>
    <p class="muted">Store {{.Location.Number}}</p>
  </div>
</div>
<nav class="portal-menu">
  <a href="/locations/{{.Location.ID}}">Overview</a>
  <a href="/locations/{{.Location.ID}}/details">Employee Details</a>
  <a href="/locations/{{.Location.ID}}/pay">Employee Pay</a>
  <a href="/locations/{{.Location.ID}}/calendar">Calendar</a>
  <a class="active" href="/locations/{{.Location.ID}}/sales">Sales</a>
  <a href="/locations/{{.Location.ID}}/productivity">Productivity</a>
  <a href="/locations/{{.Location.ID}}/documents">Documents</a>
  <a href="/locations/{{.Location.ID}}/edit">Edit</a>
  <a href="/locations/{{.Location.ID}}/labor">Labor</a>
  <a href="/locations/{{.Location.ID}}/departments">Departments</a>
  <a href="/locations/{{.Location.ID}}/roles">Roles</a>
</nav>
<form method="get" action="/locations/{{.Location.ID}}/sales" class="panel inline">
  <label>Start <input type="date" name="start" value="{{.StartDate}}" required></label>
  <label>End <input type="date" name="end" value="{{.EndDate}}" required></label>
  <button>Generate sales report</button>
</form>
{{if .Complete}}
  <section class="overview-grid">
    <article class="metric"><span>Required days</span><strong>{{.SelectedDateCount}}</strong></article>
    <article class="metric"><span>Imported days</span><strong>{{len .DailyRows}}</strong></article>
    <article class="metric"><span>Total sales</span><strong>{{formatMoney (salesRowsTotal .DaypartRows)}}</strong></article>
    <article class="metric"><span>Average sales</span><strong>{{formatMoney .SalesBenchmark.AverageCents}}</strong><em>{{.SalesBenchmark.IncludedDays}} benchmark days</em></article>
    <article class="metric"><span>Range</span><strong>{{formatISODate .StartDate}}</strong><em>{{formatISODate .EndDate}}</em></article>
  </section>
  {{if .SalesChart}}
    <section>
      <div class="productivity-chart-wrap">
        <canvas id="sales-chart" class="productivity-chart" width="1040" height="430" aria-label="Daily sales versus average sales chart"></canvas>
      </div>
    </section>
  {{end}}
  {{if .SalesBenchmark.ExcludedDates}}
    <section class="notice">
      <strong>Some dates were excluded from the average line.</strong>
      <p>Zero or unusually low sales days: {{range .SalesBenchmark.ExcludedDates}}<a class="missing-date-link" href="{{calendarDayPath $.Location.ID .}}">{{formatISODate .}}</a> {{end}}</p>
    </section>
  {{end}}
  <section>
    <h2>Sales by day</h2>
    <table><thead><tr><th>Date</th><th>Day</th><th>Total</th><th>Breakfast</th><th>Lunch</th><th>Afternoon</th><th>Dinner</th></tr></thead><tbody>{{range .DailyRows}}<tr><td><a class="table-link" href="{{calendarDayPath $.Location.ID .Date}}">{{.DateLabel}}</a></td><td>{{.Weekday}}</td><td>{{formatMoney .TotalCents}}</td>{{range .Dayparts}}<td>{{formatMoney .Cents}}</td>{{end}}</tr>{{else}}<tr><td colspan="7">No sales found.</td></tr>{{end}}</tbody></table>
  </section>
  <section class="split">
    <div>
      <h2>Sales by daypart</h2>
      <table><tbody>{{range .DaypartRows}}<tr><td>{{.Label}}</td><td>{{formatMoney .Cents}}</td><td>{{.Percent}}</td></tr>{{end}}</tbody></table>
    </div>
    <div>
      <h2>Sales by destination</h2>
      <table><tbody>{{range .DestinationRows}}<tr><td>{{.Label}}</td><td>{{formatMoney .Cents}}</td><td>{{.Percent}}</td></tr>{{end}}</tbody></table>
    </div>
  </section>
  <section>
    <h2>Sales by day of week</h2>
    <table><tbody>{{range .DayOfWeekRows}}<tr><td>{{.Label}}</td><td>{{formatMoney .Cents}}</td><td>{{.Percent}}</td></tr>{{end}}</tbody></table>
  </section>
{{else}}
  <section class="notice bad">
    <strong>Sales data is incomplete for this range.</strong>
    <p>Missing required non-Sunday dates: {{range .MissingDates}}<a class="missing-date-link" href="{{calendarDayPath $.Location.ID .}}">{{formatISODate .}}</a> {{end}}</p>
  </section>
{{end}}
<script>
(() => {
  const canvas = document.getElementById('sales-chart');
  if (!canvas) return;
  const points = {{formatChartJSON .SalesChart}};
  if (!points.length) return;
  const parentWidth = canvas.parentElement ? canvas.parentElement.clientWidth : canvas.clientWidth;
  canvas.style.width = Math.max(parentWidth, Math.min(3600, Math.max(720, points.length * 8))) + 'px';
  const ctx = canvas.getContext('2d');
  const ratio = window.devicePixelRatio || 1;
  const cssWidth = canvas.clientWidth || canvas.width;
  const cssHeight = canvas.clientHeight || canvas.height;
  canvas.width = Math.round(cssWidth * ratio);
  canvas.height = Math.round(cssHeight * ratio);
  ctx.scale(ratio, ratio);

  const pad = {top: 26, right: 26, bottom: 72, left: 74};
  const width = cssWidth - pad.left - pad.right;
  const height = cssHeight - pad.top - pad.bottom;
  const values = points.flatMap(point => [point.actual, point.average]);
  let min = Math.floor((Math.min(...values) - 250) / 250) * 250;
  let max = Math.ceil((Math.max(...values) + 250) / 250) * 250;
  if (min < 0) min = 0;
  if (min === max) max = min + 1000;
  const x = index => pad.left + (points.length === 1 ? width / 2 : index * width / (points.length - 1));
  const y = value => pad.top + (max - value) * height / (max - min);
  const money = value => '$' + Math.round(value).toLocaleString();

  ctx.clearRect(0, 0, cssWidth, cssHeight);
  ctx.fillStyle = '#111';
  ctx.fillRect(0, 0, cssWidth, cssHeight);
  ctx.strokeStyle = '#303030';
  ctx.fillStyle = '#a3a3a3';
  ctx.font = '12px system-ui, -apple-system, Segoe UI, sans-serif';
  ctx.textAlign = 'right';
  ctx.textBaseline = 'middle';
  for (let step = 0; step <= 5; step++) {
    const value = min + (max - min) * step / 5;
    const yy = y(value);
    ctx.beginPath();
    ctx.moveTo(pad.left, yy);
    ctx.lineTo(pad.left + width, yy);
    ctx.stroke();
    ctx.fillText(money(value), pad.left - 10, yy);
  }

  const drawLine = (key, color, dash = []) => {
    ctx.save();
    ctx.strokeStyle = color;
    ctx.lineWidth = 2;
    ctx.setLineDash(dash);
    ctx.beginPath();
    points.forEach((point, index) => {
      const xx = x(index);
      const yy = y(point[key]);
      if (index === 0) ctx.moveTo(xx, yy); else ctx.lineTo(xx, yy);
    });
    ctx.stroke();
    ctx.restore();
  };
  drawLine('average', '#3b82f6', [6, 5]);
  drawLine('actual', '#f97316');

  points.forEach((point, index) => {
    const xx = x(index);
    if (points.length <= 100 || point.label) {
      ctx.fillStyle = '#f97316';
      ctx.beginPath();
      ctx.arc(xx, y(point.actual), 4, 0, Math.PI * 2);
      ctx.fill();
    }
    ctx.save();
    ctx.translate(xx, pad.top + height + 14);
    ctx.rotate(-Math.PI / 2);
    ctx.textAlign = 'right';
    ctx.fillStyle = '#a3a3a3';
    ctx.fillText(point.label, 0, 4);
    ctx.restore();
  });

  ctx.fillStyle = '#f5f5f5';
  ctx.font = '700 14px system-ui, -apple-system, Segoe UI, sans-serif';
  ctx.textAlign = 'left';
  ctx.fillText('Daily sales', pad.left, 18);
  ctx.fillStyle = '#f97316';
  ctx.fillRect(pad.left + 78, 10, 18, 3);
  ctx.fillStyle = '#f5f5f5';
  ctx.fillText('Average', pad.left + 114, 18);
  ctx.fillStyle = '#3b82f6';
  ctx.fillRect(pad.left + 176, 10, 18, 3);
})();
</script>
{{end}}`

const locationProductivityHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>{{.Location.Name}} Productivity</h1>
    <p class="muted">Store {{.Location.Number}} | {{.Report.RangeLabel}}</p>
  </div>
</div>
<nav class="portal-menu">
  <a href="/locations/{{.Location.ID}}">Overview</a>
  <a href="/locations/{{.Location.ID}}/details">Employee Details</a>
  <a href="/locations/{{.Location.ID}}/pay">Employee Pay</a>
  <a href="/locations/{{.Location.ID}}/calendar?month={{.Report.Month}}">Calendar</a>
  <a href="/locations/{{.Location.ID}}/sales">Sales</a>
  <a class="active" href="/locations/{{.Location.ID}}/productivity?month={{.Report.Month}}">Productivity</a>
  <a href="/locations/{{.Location.ID}}/documents">Documents</a>
  <a href="/locations/{{.Location.ID}}/edit">Edit</a>
  <a href="/locations/{{.Location.ID}}/labor">Labor</a>
  <a href="/locations/{{.Location.ID}}/departments">Departments</a>
  <a href="/locations/{{.Location.ID}}/roles">Roles</a>
</nav>
<section class="panel">
  <div class="calendar-head">
    <a class="button secondary" href="/locations/{{.Location.ID}}/productivity?month={{.Report.PrevMonth}}">Previous</a>
    <h2>{{.Report.RangeLabel}}</h2>
    <a class="button secondary" href="/locations/{{.Location.ID}}/productivity?month={{.Report.NextMonth}}">Next</a>
  </div>
  <form method="get" action="/locations/{{.Location.ID}}/productivity" class="range-form">
    <label>Start date
      <input type="date" name="start_date" value="{{.Report.StartDate}}">
    </label>
    <label>End date
      <input type="date" name="end_date" value="{{.Report.EndDate}}">
    </label>
    <button type="submit">View range</button>
    <a class="button secondary" href="/locations/{{.Location.ID}}/productivity">Current month</a>
  </form>
  {{if or .Report.GoalDisplayValue .Report.Chart}}
    <div class="overview-grid">
      <article class="metric"><span>Completed days</span><strong>{{len .Report.Rows}}</strong></article>
      <article class="metric"><span>Total sales</span><strong>{{formatMoney .Report.TotalSalesCents}}</strong></article>
      <article class="metric"><span>Time punch hours</span><strong>{{formatHours .Report.TotalLaborMinutes}}</strong></article>
      <article class="metric"><span>Average productivity</span><strong>{{formatProductivity .Report.AverageBasisPoints}}</strong><em>{{if .Report.IsRange}}Targets follow each month{{else}}Goal {{.Report.GoalDisplayValue}}{{end}}</em></article>
    </div>
    {{if .Report.Chart}}
      <div class="productivity-chart-wrap">
        <canvas id="productivity-chart" class="productivity-chart" width="1040" height="430" aria-label="Productivity versus target chart"></canvas>
      </div>
    {{else}}
      <p class="empty">No complete sales and labor days are available for this range yet.</p>
    {{end}}
  {{else}}
    <p class="notice bad">Set a productivity goal on the {{.Report.MonthLabel}} calendar before generating this graph.</p>
  {{end}}
</section>
{{if .Report.MissingGoalMonths}}
<section class="notice bad">
  <strong>Some months need productivity goals.</strong>
  <p>Set goals for: {{range .Report.MissingGoalMonths}}<a class="missing-date-link" href="/locations/{{$.Location.ID}}/calendar?month={{.}}">{{.}}</a> {{end}}</p>
</section>
{{end}}
{{if .Report.MissingDates}}
<section class="notice bad">
  <strong>Some dates are not included in the graph.</strong>
  <p>Missing sales, labor, or time punch hours: {{range .Report.MissingDates}}<a class="missing-date-link" href="{{calendarDayPath $.Location.ID .}}">{{formatISODate .}}</a> {{end}}</p>
</section>
{{end}}
{{if .Report.Rows}}
<section>
  <h2>Daily productivity</h2>
  <table><thead><tr><th>Date</th><th>Day</th><th>Sales</th><th>Time punch hours</th><th>Productivity</th><th>Target</th><th>Gap</th></tr></thead><tbody>{{range .Report.Rows}}<tr><td><a class="table-link" href="{{calendarDayPath $.Location.ID .Date}}">{{.DateLabel}}</a></td><td>{{.Weekday}}</td><td>{{formatMoney .SalesCents}}</td><td>{{.LaborHours}}</td><td>{{.ProductivityDisplay}}</td><td>{{.TargetDisplay}}</td><td class="{{if lt .GapBasisPoints 0}}negative{{else}}positive{{end}}">{{.GapDisplay}}</td></tr>{{end}}</tbody></table>
</section>
{{end}}
<script>
(() => {
  const canvas = document.getElementById('productivity-chart');
  if (!canvas) return;
  const points = {{formatChartJSON .Report.Chart}};
  if (!points.length) return;
  const parentWidth = canvas.parentElement ? canvas.parentElement.clientWidth : canvas.clientWidth;
  canvas.style.width = Math.max(parentWidth, Math.min(3600, Math.max(720, points.length * 8))) + 'px';
  const ctx = canvas.getContext('2d');
  const ratio = window.devicePixelRatio || 1;
  const cssWidth = canvas.clientWidth || canvas.width;
  const cssHeight = canvas.clientHeight || canvas.height;
  canvas.width = Math.round(cssWidth * ratio);
  canvas.height = Math.round(cssHeight * ratio);
  ctx.scale(ratio, ratio);

  const pad = {top: 26, right: 26, bottom: 72, left: 58};
  const width = cssWidth - pad.left - pad.right;
  const height = cssHeight - pad.top - pad.bottom;
  const values = points.flatMap(point => [point.actual, point.target]);
  let min = Math.floor((Math.min(...values) - 5) / 5) * 5;
  let max = Math.ceil((Math.max(...values) + 5) / 5) * 5;
  if (min === max) max = min + 10;
  const x = index => pad.left + (points.length === 1 ? width / 2 : index * width / (points.length - 1));
  const y = value => pad.top + (max - value) * height / (max - min);

  ctx.clearRect(0, 0, cssWidth, cssHeight);
  ctx.fillStyle = '#111';
  ctx.fillRect(0, 0, cssWidth, cssHeight);
  ctx.strokeStyle = '#303030';
  ctx.fillStyle = '#a3a3a3';
  ctx.font = '12px system-ui, -apple-system, Segoe UI, sans-serif';
  ctx.textAlign = 'right';
  ctx.textBaseline = 'middle';
  for (let step = 0; step <= 5; step++) {
    const value = min + (max - min) * step / 5;
    const yy = y(value);
    ctx.beginPath();
    ctx.moveTo(pad.left, yy);
    ctx.lineTo(pad.left + width, yy);
    ctx.stroke();
    ctx.fillText(value.toFixed(0), pad.left - 10, yy);
  }

  for (let i = 0; i < points.length - 1; i++) {
    ctx.beginPath();
    ctx.moveTo(x(i), y(points[i].target));
    ctx.lineTo(x(i), y(points[i].actual));
    ctx.lineTo(x(i + 1), y(points[i + 1].actual));
    ctx.lineTo(x(i + 1), y(points[i + 1].target));
    ctx.closePath();
    ctx.fillStyle = ((points[i].actual + points[i + 1].actual) / 2 >= (points[i].target + points[i + 1].target) / 2) ? 'rgba(34,197,94,.18)' : 'rgba(239,68,68,.18)';
    ctx.fill();
  }

  const drawLine = (key, color, dash = []) => {
    ctx.save();
    ctx.strokeStyle = color;
    ctx.lineWidth = 2;
    ctx.setLineDash(dash);
    ctx.beginPath();
    points.forEach((point, index) => {
      const xx = x(index);
      const yy = y(point[key]);
      if (index === 0) ctx.moveTo(xx, yy); else ctx.lineTo(xx, yy);
    });
    ctx.stroke();
    ctx.restore();
  };
  drawLine('target', '#3b82f6', [6, 5]);
  drawLine('actual', '#f97316');

  points.forEach((point, index) => {
    const xx = x(index);
    if (points.length <= 100 || point.label) {
      ctx.fillStyle = '#3b82f6';
      ctx.beginPath();
      ctx.arc(xx, y(point.target), 4, 0, Math.PI * 2);
      ctx.fill();
      ctx.fillStyle = '#f97316';
      ctx.beginPath();
      ctx.arc(xx, y(point.actual), 4, 0, Math.PI * 2);
      ctx.fill();
    }
    ctx.save();
    ctx.translate(xx, pad.top + height + 14);
    ctx.rotate(-Math.PI / 2);
    ctx.textAlign = 'right';
    ctx.fillStyle = '#a3a3a3';
    ctx.fillText(point.label, 0, 4);
    ctx.restore();
  });

  ctx.fillStyle = '#f5f5f5';
  ctx.font = '700 14px system-ui, -apple-system, Segoe UI, sans-serif';
  ctx.textAlign = 'left';
  ctx.fillText('Actual productivity', pad.left, 18);
  ctx.fillStyle = '#f97316';
  ctx.fillRect(pad.left + 138, 10, 18, 3);
  ctx.fillStyle = '#f5f5f5';
  ctx.fillText('Target', pad.left + 174, 18);
  ctx.fillStyle = '#3b82f6';
  ctx.fillRect(pad.left + 226, 10, 18, 3);
})();
</script>
{{end}}`

const locationDocumentsHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>{{.Location.Name}} Documents</h1>
    <p class="muted">Store {{.Location.Number}}</p>
  </div>
</div>
<nav class="portal-menu">
  <a href="/locations/{{.Location.ID}}">Overview</a>
  <a href="/locations/{{.Location.ID}}/details">Employee Details</a>
  <a href="/locations/{{.Location.ID}}/pay">Employee Pay</a>
  <a href="/locations/{{.Location.ID}}/calendar">Calendar</a>
  <a href="/locations/{{.Location.ID}}/sales">Sales</a>
  <a class="active" href="/locations/{{.Location.ID}}/documents">Documents</a>
  <a href="/locations/{{.Location.ID}}/edit">Edit</a>
  <a href="/locations/{{.Location.ID}}/labor">Labor</a>
  <a href="/locations/{{.Location.ID}}/departments">Departments</a>
  <a href="/locations/{{.Location.ID}}/roles">Roles</a>
</nav>
{{if .Import.Get "added"}}<p class="notice">Employee bio imported for {{.Location.Name}}. Added {{.Import.Get "added"}}, updated {{.Import.Get "updated"}}, removed {{.Import.Get "removed"}}, skipped {{.Import.Get "skipped"}}.</p>{{end}}
{{if .Import.Get "birthday_updated"}}<p class="notice">Birthday report imported for {{.Location.Name}}. Updated {{.Import.Get "birthday_updated"}} employee records. Skipped {{.Import.Get "birthday_skipped"}} rows that did not match current employees at this location.</p>{{end}}
{{if .Import.Get "pin_updated"}}<p class="notice">PIN report imported for {{.Location.Name}}. Updated {{.Import.Get "pin_updated"}} employee records. Skipped {{.Import.Get "pin_skipped"}} rows that did not match current employees at this location.</p>{{end}}
{{if .Import.Get "wages_imported"}}<p class="notice">Time punch wages imported for {{.Location.Name}}.</p>{{end}}
<section class="split">
  <form method="post" action="/locations/{{.Location.ID}}/upload" enctype="multipart/form-data" class="panel">
    <h2>Upload employee bio</h2>
    <p class="muted">This syncs active employees for this location and removes terminated or missing employees.</p>
    <input type="file" name="bio" accept=".xlsx" required>
    <button>Upload .xlsx</button>
  </form>
  <form method="post" action="/locations/{{.Location.ID}}/birthdays/upload" enctype="multipart/form-data" class="panel">
    <h2>Upload birthday report</h2>
    <p class="muted">This applies Employee Birthday Reader rows only to matching employees at this location.</p>
    <input type="file" name="birthdays" accept=".xlsx" required>
    <button>Upload birthdays</button>
  </form>
  <form method="post" action="/locations/{{.Location.ID}}/pins/upload" enctype="multipart/form-data" class="panel">
    <h2>Upload PIN report</h2>
    <p class="muted">This applies clock-in PINs only to matching employees at this location.</p>
    <input type="file" name="pins" accept=".pdf" required>
    <button>Upload PINs</button>
  </form>
  <form method="post" action="/locations/{{.Location.ID}}/documents/time-punch-wages" enctype="multipart/form-data" class="panel">
    <h2>Upload time punch wages</h2>
    <p class="muted">This reads wage rates from the time punch report and updates employee pay details.</p>
    <input type="file" name="time_punch" accept=".pdf" required>
    <button>Import wages</button>
  </form>
</section>
{{end}}`

const locationEditHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>Edit {{.Location.Name}}</h1>
    <p class="muted">Store {{.Location.Number}}</p>
  </div>
</div>
<nav class="portal-menu">
  <a href="/locations/{{.Location.ID}}">Overview</a>
  <a href="/locations/{{.Location.ID}}/details">Employee Details</a>
  <a href="/locations/{{.Location.ID}}/pay">Employee Pay</a>
  <a href="/locations/{{.Location.ID}}/calendar">Calendar</a>
  <a href="/locations/{{.Location.ID}}/sales">Sales</a>
  <a href="/locations/{{.Location.ID}}/documents">Documents</a>
  <a class="active" href="/locations/{{.Location.ID}}/edit">Edit</a>
  <a href="/locations/{{.Location.ID}}/labor">Labor</a>
  <a href="/locations/{{.Location.ID}}/departments">Departments</a>
  <a href="/locations/{{.Location.ID}}/roles">Roles</a>
</nav>
{{if .Saved}}<p class="notice">Location saved.</p>{{end}}
<section class="split">
  <form method="post" action="/locations/{{.Location.ID}}" class="panel">
    <h2>Edit location</h2>
    <label>Name <input name="name" value="{{.Location.Name}}" required></label>
    <label>Store number <input name="number" value="{{.Location.Number}}" required></label>
    <label>Store email <input type="email" name="email" value="{{.Location.Email}}" required></label>
    <button>Save</button>
  </form>
  <section class="panel danger-zone">
    <h2>Delete location</h2>
    <p class="muted">Deleting a location also deletes its employee records.</p>
    <form method="post" action="/locations/{{.Location.ID}}/delete" onsubmit="return confirm('Delete this location and its employees?')"><button class="danger">Delete location</button></form>
  </section>
</section>
{{end}}`

const employeeProfileHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>{{.Employee.EmployeeName}}</h1>
    <p class="muted">Store {{.Location.Number}} | Employee # {{.Employee.EmployeeNumber}}</p>
  </div>
  <a class="button secondary" href="/locations/{{.Location.ID}}">Back to employees</a>
</div>
<nav class="portal-menu">
  <a href="/locations/{{.Location.ID}}">Overview</a>
  <a href="/locations/{{.Location.ID}}/details">Employee Details</a>
  <a href="/locations/{{.Location.ID}}/pay">Employee Pay</a>
  <a href="/locations/{{.Location.ID}}/calendar">Calendar</a>
  <a href="/locations/{{.Location.ID}}/sales">Sales</a>
  <a href="/locations/{{.Location.ID}}/documents">Documents</a>
  <a href="/locations/{{.Location.ID}}/edit">Edit</a>
  <a href="/locations/{{.Location.ID}}/labor">Labor</a>
  <a href="/locations/{{.Location.ID}}/departments">Departments</a>
  <a href="/locations/{{.Location.ID}}/roles">Roles</a>
</nav>
<section class="panel">
  <h2>Employee details</h2>
  <p><strong>Job:</strong> {{.Employee.Job}}</p>
  <p><strong>Role:</strong> {{if .Employee.RoleName}}{{.Employee.RoleName}}{{else}}<span class="muted">Unassigned</span>{{end}}</p>
  <p><strong>Department:</strong> {{if .Employee.DepartmentName}}{{.Employee.DepartmentName}}{{else}}<span class="muted">Unassigned</span>{{end}}</p>
  <p><strong>Status:</strong> {{.Employee.EmployeeStatus}}</p>
  <p><strong>Start date:</strong> {{.Employee.LocationLatestStartDate}}</p>
  <p><strong>Birth date:</strong> {{if .Employee.BirthDate}}{{.Employee.BirthDate}}{{else}}<span class="muted">Not imported</span>{{end}}</p>
  <p><strong>Clock-in PIN:</strong> {{if .Employee.ClockInPIN}}{{.Employee.ClockInPIN}}{{else}}<span class="muted">Not imported</span>{{end}}</p>
</section>
{{end}}`

const laborHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>{{.SelectedLocation.Name}} Labor</h1>
    <p class="muted">Store {{.SelectedLocation.Number}}</p>
  </div>
</div>
<nav class="portal-menu">
  <a href="/locations/{{.SelectedLocation.ID}}">Overview</a>
  <a href="/locations/{{.SelectedLocation.ID}}/details">Employee Details</a>
  <a href="/locations/{{.SelectedLocation.ID}}/pay">Employee Pay</a>
  <a href="/locations/{{.SelectedLocation.ID}}/calendar">Calendar</a>
  <a href="/locations/{{.SelectedLocation.ID}}/sales">Sales</a>
  <a href="/locations/{{.SelectedLocation.ID}}/productivity">Productivity</a>
  <a href="/locations/{{.SelectedLocation.ID}}/documents">Documents</a>
  <a href="/locations/{{.SelectedLocation.ID}}/edit">Edit</a>
  <a class="active" href="/locations/{{.SelectedLocation.ID}}/labor">Labor</a>
  <a href="/locations/{{.SelectedLocation.ID}}/departments">Departments</a>
  <a href="/locations/{{.SelectedLocation.ID}}/roles">Roles</a>
</nav>
<form method="get" action="/locations/{{.SelectedLocation.ID}}/labor" class="panel inline">
  <label>Start <input type="date" name="start" value="{{.StartDate}}" required></label>
  <label>End <input type="date" name="end" value="{{.EndDate}}" required></label>
  <button>Generate labor report</button>
</form>
{{if .Complete}}
  <section class="overview-grid">
    {{range .LaborSummary}}<article class="metric"><span>{{.Label}}</span><strong>{{.Hours}}</strong><em>{{.Dollars}}</em>{{if .Detail}}<em>{{.Detail}}</em>{{end}}</article>{{end}}
  </section>
{{else}}
  <section class="notice bad">
    <strong>Labor data is incomplete for this range.</strong>
    <p>Missing required non-Sunday dates: {{range .MissingDates}}<a class="missing-date-link" href="{{calendarDayPath $.SelectedLocation.ID .}}">{{formatISODate .}}</a> {{end}}</p>
  </section>
{{end}}
<form method="post" action="/locations/{{.SelectedLocation.ID}}/labor" enctype="multipart/form-data" class="panel labor-upload">
  <label>Analyze a time punch report
    <input type="file" name="time_punch" accept=".pdf" required>
  </label>
  <button>Analyze labor</button>
</form>
{{if .Report}}
<section class="report-head">
  <div>
    <h2>{{.SelectedLocation.Name}}</h2>
    <p class="muted">Store {{.SelectedLocation.Number}}{{if .Report.LocationName}} · Report location: {{.Report.LocationName}}{{end}}</p>
    {{if .Report.PeriodLabel}}<p class="muted">{{.Report.PeriodLabel}}</p>{{end}}
  </div>
  <div class="summary-grid">
    {{range .Summary}}<article class="metric"><span>{{.Label}}</span><strong>{{.Hours}}</strong><em>{{.Dollars}}</em>{{if .Detail}}<em>{{.Detail}}</em>{{end}}</article>{{end}}
  </div>
</section>
<section>
  <h2>Labor by day of week</h2>
  <table>
    <thead><tr><th>Day</th><th>Dates included</th><th>Hours</th><th>Labor dollars</th><th>Total labor</th></tr></thead>
    <tbody>{{range .DayRows}}<tr><td>{{.Day}}</td><td>{{.Date}}</td><td>{{.Hours}}</td><td>{{.Dollars}}</td><td>{{.Percent}}</td></tr>{{else}}<tr><td colspan="5">No day labor found.</td></tr>{{end}}</tbody>
  </table>
</section>
<section>
  <h2>Labor by role</h2>
  <table>
    <thead><tr><th>Role</th><th>Hours</th><th>Labor dollars</th><th>Total labor</th></tr></thead>
    <tbody>{{range .RoleRows}}<tr><td>{{.Role}}</td><td>{{.Hours}}</td><td>{{.Dollars}}</td><td>{{.Percent}}</td></tr>{{else}}<tr><td colspan="4">No role labor found.</td></tr>{{end}}</tbody>
  </table>
</section>
<section>
  <h2>Labor by department</h2>
  <table>
    <thead><tr><th>Department</th><th>Hours</th><th>Labor dollars</th><th>Total labor</th></tr></thead>
    <tbody>{{range .DepartmentRows}}<tr><td>{{.Department}}</td><td>{{.Hours}}</td><td>{{.Dollars}}</td><td>{{.Percent}}</td></tr>{{else}}<tr><td colspan="4">No department labor found.</td></tr>{{end}}</tbody>
  </table>
</section>
<section>
  <h2>Labor by job</h2>
  <table>
    <thead><tr><th>Job</th><th>Hours</th><th>Labor dollars</th><th>Total labor</th></tr></thead>
    <tbody>{{range .JobRows}}<tr><td>{{.Job}}</td><td>{{.Hours}}</td><td>{{.Dollars}}</td><td>{{.Percent}}</td></tr>{{else}}<tr><td colspan="4">No job labor found.</td></tr>{{end}}</tbody>
  </table>
</section>
<section id="employee-labor">
  <div class="section-head">
    <h2>Labor by employee</h2>
    <div class="labor-controls">
      <label>Name
        <input id="employee-search" type="search" placeholder="Filter by name">
      </label>
      <label>Job
        <select id="employee-job-filter">
          <option value="">All jobs</option>
          {{range .EmployeeJobs}}<option value="{{.}}">{{.}}</option>{{end}}
        </select>
      </label>
      <label>Sort
        <select id="employee-sort">
          <option value="hours_desc">Hours high to low</option>
          <option value="hours_asc">Hours low to high</option>
          <option value="name_asc">Name A-Z</option>
          <option value="name_desc">Name Z-A</option>
          <option value="job_asc">Job A-Z</option>
          <option value="dollars_desc">Labor dollars high to low</option>
        </select>
      </label>
    </div>
  </div>
  <table>
    <thead><tr><th>Employee</th><th>Role</th><th>Department</th><th>Job</th><th>Hours</th><th>Labor dollars</th></tr></thead>
    <tbody id="employee-labor-rows">{{range .EmployeeRows}}<tr data-name="{{.Name}}" data-job="{{.Job}}" data-minutes="{{.MinutesValue}}" data-cents="{{.CentsValue}}"><td>{{.Name}}</td><td>{{.Role}}</td><td>{{.Department}}</td><td>{{.Job}}</td><td>{{.Hours}}</td><td>{{.Dollars}}</td></tr>{{else}}<tr><td colspan="6">No employee labor found.</td></tr>{{end}}</tbody>
  </table>
</section>
<script>
(() => {
  const tbody = document.getElementById('employee-labor-rows');
  if (!tbody) return;
  const rows = Array.from(tbody.querySelectorAll('tr[data-name]'));
  const search = document.getElementById('employee-search');
  const jobFilter = document.getElementById('employee-job-filter');
  const sort = document.getElementById('employee-sort');
  const text = (row, key) => (row.dataset[key] || '').toLowerCase();
  const number = (row, key) => Number(row.dataset[key] || 0);
  function compareRows(a, b) {
    switch (sort.value) {
    case 'hours_asc':
      return number(a, 'minutes') - number(b, 'minutes') || text(a, 'name').localeCompare(text(b, 'name'));
    case 'name_asc':
      return text(a, 'name').localeCompare(text(b, 'name'));
    case 'name_desc':
      return text(b, 'name').localeCompare(text(a, 'name'));
    case 'job_asc':
      return text(a, 'job').localeCompare(text(b, 'job')) || text(a, 'name').localeCompare(text(b, 'name'));
    case 'dollars_desc':
      return number(b, 'cents') - number(a, 'cents') || text(a, 'name').localeCompare(text(b, 'name'));
    default:
      return number(b, 'minutes') - number(a, 'minutes') || text(a, 'name').localeCompare(text(b, 'name'));
    }
  }
  function applyEmployeeControls() {
    const query = (search.value || '').trim().toLowerCase();
    const job = jobFilter.value;
    rows.sort(compareRows).forEach(row => {
      row.hidden = Boolean(query && !text(row, 'name').includes(query)) || Boolean(job && row.dataset.job !== job);
      tbody.appendChild(row);
    });
  }
  [search, jobFilter, sort].forEach(control => control.addEventListener('input', applyEmployeeControls));
  applyEmployeeControls();
})();
</script>
{{end}}
{{end}}`

const rolesHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>{{.SelectedLocation.Name}} Roles</h1>
    <p class="muted">Store {{.SelectedLocation.Number}}</p>
  </div>
</div>
<nav class="portal-menu">
  <a href="/locations/{{.SelectedLocation.ID}}">Overview</a>
  <a href="/locations/{{.SelectedLocation.ID}}/details">Employee Details</a>
  <a href="/locations/{{.SelectedLocation.ID}}/pay">Employee Pay</a>
  <a href="/locations/{{.SelectedLocation.ID}}/calendar">Calendar</a>
  <a href="/locations/{{.SelectedLocation.ID}}/sales">Sales</a>
  <a href="/locations/{{.SelectedLocation.ID}}/documents">Documents</a>
  <a href="/locations/{{.SelectedLocation.ID}}/edit">Edit</a>
  <a href="/locations/{{.SelectedLocation.ID}}/labor">Labor</a>
  <a href="/locations/{{.SelectedLocation.ID}}/departments">Departments</a>
  <a class="active" href="/locations/{{.SelectedLocation.ID}}/roles">Roles</a>
</nav>
<form method="post" action="/locations/{{.SelectedLocation.ID}}/roles/manage" class="panel inline">
  <label>Role name <input name="name" placeholder="Team Leader" required></label>
  <button>Create role</button>
</form>
<table>
  <thead><tr><th>Name</th><th>Assigned employees</th><th></th></tr></thead>
  <tbody>
  {{range .Roles}}
    <tr>
      <td>
        <form method="post" action="/locations/{{$.SelectedLocation.ID}}/roles/{{.ID}}" class="inline table-form">
          <label class="sr-only">Role name</label>
          <input name="name" value="{{.Name}}" required>
          <button class="small secondary">Save</button>
        </form>
      </td>
      <td>{{.Employees}}</td>
      <td><form method="post" action="/locations/{{$.SelectedLocation.ID}}/roles/{{.ID}}/delete" onsubmit="return confirm('Delete this role and clear it from assigned employees?')"><button class="danger small">Delete</button></form></td>
    </tr>
  {{else}}
    <tr><td colspan="3">No roles created.</td></tr>
  {{end}}
  </tbody>
</table>
{{end}}`

const departmentsHTML = `{{define "body"}}
<div class="row">
  <div>
    <h1>{{.SelectedLocation.Name}} Departments</h1>
    <p class="muted">Store {{.SelectedLocation.Number}}</p>
  </div>
</div>
<nav class="portal-menu">
  <a href="/locations/{{.SelectedLocation.ID}}">Overview</a>
  <a href="/locations/{{.SelectedLocation.ID}}/details">Employee Details</a>
  <a href="/locations/{{.SelectedLocation.ID}}/pay">Employee Pay</a>
  <a href="/locations/{{.SelectedLocation.ID}}/calendar">Calendar</a>
  <a href="/locations/{{.SelectedLocation.ID}}/sales">Sales</a>
  <a href="/locations/{{.SelectedLocation.ID}}/documents">Documents</a>
  <a href="/locations/{{.SelectedLocation.ID}}/edit">Edit</a>
  <a href="/locations/{{.SelectedLocation.ID}}/labor">Labor</a>
  <a class="active" href="/locations/{{.SelectedLocation.ID}}/departments">Departments</a>
  <a href="/locations/{{.SelectedLocation.ID}}/roles">Roles</a>
</nav>
<form method="post" action="/locations/{{.SelectedLocation.ID}}/departments/manage" class="panel inline">
  <label>Department name <input name="name" placeholder="Front of House" required></label>
  <button>Create department</button>
</form>
<table>
  <thead><tr><th>Name</th><th>Assigned employees</th><th></th></tr></thead>
  <tbody>
  {{range .Departments}}
    <tr>
      <td>
        <form method="post" action="/locations/{{$.SelectedLocation.ID}}/departments/{{.ID}}" class="inline table-form">
          <label class="sr-only">Department name</label>
          <input name="name" value="{{.Name}}" required>
          <button class="small secondary">Save</button>
        </form>
      </td>
      <td>{{.Employees}}</td>
      <td><form method="post" action="/locations/{{$.SelectedLocation.ID}}/departments/{{.ID}}/delete" onsubmit="return confirm('Delete this department and clear it from assigned employees?')"><button class="danger small">Delete</button></form></td>
    </tr>
  {{else}}
    <tr><td colspan="3">No departments created.</td></tr>
  {{end}}
  </tbody>
</table>
{{end}}`

const tokensHTML = `{{define "body"}}
<div class="row"><h1>API Tokens</h1></div>
{{if .NewToken}}<section class="notice"><strong>{{.NewTokenName}}</strong><code>{{.NewToken}}</code><p>Copy this now. It will not be shown again.</p></section>{{end}}
<form method="post" action="/tokens" class="panel inline">
  <label>Name <input name="name" required></label>
  <button>Create token</button>
</form>
<table>
  <thead><tr><th>Name</th><th>Prefix</th><th>Created</th><th>Last used</th><th></th></tr></thead>
  <tbody>{{range .Tokens}}<tr><td>{{.Name}}</td><td>{{.Prefix}}</td><td>{{.CreatedAt}}</td><td>{{if .LastUsedAt}}{{.LastUsedAt}}{{end}}</td><td><form method="post" action="/tokens/{{.ID}}/delete"><button class="danger small">Delete</button></form></td></tr>{{else}}<tr><td colspan="5">No tokens created.</td></tr>{{end}}</tbody>
</table>
{{end}}`

const docsHTML = `{{define "body"}}
<h1>API Docs</h1>
<section class="panel">
  <h2>Authentication</h2>
  <p>Use <code>Authorization: Bearer &lt;token&gt;</code> or <code>X-API-Token: &lt;token&gt;</code>.</p>
</section>
<section class="panel">
  <h2>Endpoints</h2>
  <table>
    <thead><tr><th>Method</th><th>URL</th><th>Returns</th></tr></thead>
    <tbody>
      <tr><td>GET</td><td><code>{{.BaseURL}}/api/v1/locations</code></td><td>Locations with store numbers and employee counts.</td></tr>
      <tr><td>GET</td><td><code>{{.BaseURL}}/api/v1/locations/{storeNumber}/employees/identity</code></td><td>Basic identity and birthday fields for active employees.</td></tr>
      <tr><td>GET</td><td><code>{{.BaseURL}}/api/v1/locations/{storeNumber}/employees</code></td><td>Full active employees for one store, including sensitive fields such as wages and imported PINs.</td></tr>
      <tr><td>GET</td><td><code>{{.BaseURL}}/api/v1/locations/{storeNumber}/employees/{employeeNumber}/identity</code></td><td>Basic identity and birthday fields for one employee.</td></tr>
      <tr><td>GET</td><td><code>{{.BaseURL}}/api/v1/locations/{storeNumber}/employees/{employeeNumber}</code></td><td>One full active employee by employee number.</td></tr>
    </tbody>
  </table>
</section>
<section class="panel">
  <h2>Employee assignments</h2>
  <p>Roles and departments are created per location by the admin in cfasuite-hr and assigned manually to employees. The imported <code>job</code> field remains separate from role and department assignments. Employees without an assignment return <code>null</code> for those fields.</p>
</section>
<section class="panel">
  <h2>Employee birthdays</h2>
  <p>Birthdays are imported from an Employee Birthday Reader .xlsx file with <code>Employee Name</code> and <code>Birth Date</code> columns. The API returns <code>birth_date</code> as <code>YYYY-MM-DD</code> when known and <code>null</code> when no birthday has been imported for that employee.</p>
</section>
<section class="panel">
  <h2>Employee PINs</h2>
  <p>Clock-in PINs are imported from a location PIN report PDF with employee name, access level, clock-in PIN, and sign-in PIN columns. The importer ignores sign-in PINs and matches current employees at that location using normalized employee names.</p>
</section>
{{end}}`

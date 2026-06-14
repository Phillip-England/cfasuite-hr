file: /Users/phillipengland/Desktop/src/cfatp/src/lib.rs
use std::collections::BTreeMap;
use std::fs;
use std::path::Path;
use std::sync::OnceLock;
use chrono::{Datelike, NaiveDate, NaiveTime, Timelike, Weekday};
use regex::Regex;
use rust_decimal::Decimal;
use serde::Serialize;
const WORK_TYPES: &[PayType] = &[PayType::Regular];
const UNPAID_BREAK_TYPES: &[PayType] = &[PayType::Unpaid];
const NON_COMPLIANT_BREAK_TYPES: &[PayType] = &[PayType::BreakConvertedToPaid];
const SPLIT_SHIFT_GAP_MINUTES: u32 = 60;
pub enum ParseError {
    Io(std::io::Error),
    Pdf(pdf_extract::OutputError),
    InvalidDate {
        value: String,
        source: chrono::ParseError,
    },
    InvalidTime {
        value: String,
        source: chrono::ParseError,
    },
    InvalidMoney {
        value: String,
        source: rust_decimal::Error,
    },
}
impl std::fmt::Display for ParseError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Io(err) => write!(f, "I/O error: {err}"),
            Self::Pdf(err) => write!(f, "PDF extraction error: {err}"),
            Self::InvalidDate { value, .. } => write!(f, "invalid date: {value}"),
            Self::InvalidTime { value, .. } => write!(f, "invalid time: {value}"),
            Self::InvalidMoney { value, .. } => write!(f, "invalid money amount: {value}"),
        }
    }
}
impl std::error::Error for ParseError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::Io(err) => Some(err),
            Self::Pdf(err) => Some(err),
            Self::InvalidDate { source, .. } => Some(source),
            Self::InvalidTime { source, .. } => Some(source),
            Self::InvalidMoney { source, .. } => Some(source),
        }
    }
}
impl From<std::io::Error> for ParseError {
    fn from(value: std::io::Error) -> Self {
        Self::Io(value)
    }
}
impl From<pdf_extract::OutputError> for ParseError {
    fn from(value: pdf_extract::OutputError) -> Self {
        Self::Pdf(value)
    }
}
pub struct ReportPeriod {
    pub label: String,
    pub start: NaiveDate,
    pub end: NaiveDate,
}
pub struct TimePunchReport {
    pub title: String,
    pub location_name: Option<String>,
    pub period: Option<ReportPeriod>,
    pub employees: Vec<EmployeeRecord>,
    pub grand_totals: Option<EmployeeTotals>,
}
impl TimePunchReport {
    pub fn parse_file(path: impl AsRef<Path>) -> Result<Self, ParseError> {
        let bytes = fs::read(path)?;
        Self::parse_pdf_bytes(&bytes)
    }
    pub fn parse_pdf_bytes(bytes: &[u8]) -> Result<Self, ParseError> {
        let text = pdf_extract::extract_text_from_mem(bytes)?;
        Self::parse_text(&text)
    }
    pub fn parse_text(text: &str) -> Result<Self, ParseError> {
        parse_report_text(text)
    }
    pub fn employee(&self, name: &str) -> Option<&EmployeeRecord> {
        self.employees.iter().find(|employee| employee.name == name)
    }
    pub fn find_employees(&self, query: &str) -> Vec<&EmployeeRecord> {
        let query = query.to_ascii_lowercase();
        self.employees
            .iter()
            .filter(|employee| employee.name.to_ascii_lowercase().contains(&query))
            .collect()
    }
    pub fn days_for_weekday(&self, weekday: Weekday) -> Vec<DayMatch<'_>> {
        self.employees
            .iter()
            .flat_map(|employee| {
                employee
                    .days
                    .iter()
                    .filter(move |day| day.weekday == weekday)
                    .map(move |day| DayMatch { employee, day })
            })
            .collect()
    }
    pub fn clock_issues(&self) -> Vec<ClockIssue> {
        self.employees
            .iter()
            .flat_map(EmployeeRecord::clock_issues)
            .collect()
    }
    pub fn employees_with_clock_issues(&self) -> Vec<&EmployeeRecord> {
        self.employees
            .iter()
            .filter(|employee| !employee.clock_issues().is_empty())
            .collect()
    }
    pub fn break_violations(&self) -> Vec<BreakViolation> {
        self.employees
            .iter()
            .flat_map(EmployeeRecord::break_violations)
            .collect()
    }
}
pub struct EmployeeRecord {
    pub name: String,
    pub days: Vec<EmployeeDay>,
    pub totals: Option<EmployeeTotals>,
}
impl EmployeeRecord {
    pub fn day(&self, date: NaiveDate) -> Option<&EmployeeDay> {
        self.days.iter().find(|day| day.date == date)
    }
    pub fn days_for_weekday(&self, weekday: Weekday) -> Vec<&EmployeeDay> {
        self.days.iter().filter(|day| day.weekday == weekday).collect()
    }
    pub fn worked_minutes(&self) -> u32 {
        self.days.iter().map(EmployeeDay::worked_minutes).sum()
    }
    pub fn total_wages(&self) -> Option<Decimal> {
        self.totals
            .as_ref()
            .and_then(|totals| totals.total_wages)
            .or_else(|| {
                self.days
                    .iter()
                    .flat_map(|day| day.punches.iter())
                    .filter_map(|entry| entry.total_wages)
                    .fold(None, |acc, amount| Some(acc.unwrap_or_default() + amount))
            })
    }
    pub fn clock_issues(&self) -> Vec<ClockIssue> {
        self.days
            .iter()
            .flat_map(|day| {
                day.punches.iter().flat_map(|entry| {
                    let mut issues = Vec::new();
                    if entry.time_in_flagged {
                        issues.push(ClockIssue {
                            employee_name: self.name.clone(),
                            date: day.date,
                            weekday: day.weekday,
                            punch_time: entry.time_in,
                            field: ClockField::TimeIn,
                            message: "possible failure to clock in or out".to_string(),
                            raw_line: entry.raw_line.clone(),
                        });
                    }
                    if entry.time_out_flagged {
                        issues.push(ClockIssue {
                            employee_name: self.name.clone(),
                            date: day.date,
                            weekday: day.weekday,
                            punch_time: entry.time_out,
                            field: ClockField::TimeOut,
                            message: "possible failure to clock in or out".to_string(),
                            raw_line: entry.raw_line.clone(),
                        });
                    }
                    issues
                })
            })
            .collect()
    }
    pub fn shifts(&self) -> Vec<ShiftRecord> {
        self.days.iter().flat_map(EmployeeDay::split_into_shifts).collect()
    }
    pub fn break_violations(&self) -> Vec<BreakViolation> {
        self.shifts()
            .into_iter()
            .filter_map(|shift| {
                let required_breaks = determine_required_breaks(shift.worked_minutes);
                if required_breaks == 0 {
                    return None;
                }
                let actual_breaks = shift.unpaid_breaks.len() as u32;
                let converted_breaks = shift.converted_breaks.len() as u32;
                if actual_breaks >= required_breaks {
                    return None;
                }
                let mut reasons = vec![format!(
                    "shift of {} found with {} unpaid break(s); expected {} based on duration",
                    format_minutes(shift.worked_minutes),
                    actual_breaks,
                    required_breaks
                )];
                if converted_breaks > 0 {
                    reasons.push(format!(
                        "{converted_breaks} converted paid break row(s) do not count as unpaid breaks"
                    ));
                }
                Some(BreakViolation {
                    employee_name: self.name.clone(),
                    date: shift.date,
                    weekday: shift.weekday,
                    shift_span: shift.shift_span_label(),
                    worked_minutes: shift.worked_minutes,
                    required_breaks,
                    actual_breaks,
                    converted_breaks,
                    reason: reasons.join("; "),
                })
            })
            .collect()
    }
}
pub struct EmployeeDay {
    pub date: NaiveDate,
    pub weekday: Weekday,
    pub punches: Vec<PunchEntry>,
}
impl EmployeeDay {
    pub fn worked_minutes(&self) -> u32 {
        self.punches
            .iter()
            .filter(|entry| WORK_TYPES.contains(&entry.pay_type))
            .map(|entry| entry.total_minutes)
            .sum()
    }
    pub fn regular_punches(&self) -> Vec<&PunchEntry> {
        self.punches
            .iter()
            .filter(|entry| WORK_TYPES.contains(&entry.pay_type))
            .collect()
    }
    pub fn unpaid_breaks(&self) -> Vec<&PunchEntry> {
        self.punches
            .iter()
            .filter(|entry| UNPAID_BREAK_TYPES.contains(&entry.pay_type))
            .collect()
    }
    pub fn converted_breaks(&self) -> Vec<&PunchEntry> {
        self.punches
            .iter()
            .filter(|entry| NON_COMPLIANT_BREAK_TYPES.contains(&entry.pay_type))
            .collect()
    }
    pub fn shift_span_label(&self) -> Option<String> {
        let first = self
            .punches
            .iter()
            .min_by_key(|entry| entry.time_in.num_seconds_from_midnight())?;
        let last = self
            .punches
            .iter()
            .max_by_key(|entry| entry.time_out.num_seconds_from_midnight())?;
        Some(format!(
            "{} to {}",
            format_clock_time(first.time_in),
            format_clock_time(last.time_out)
        ))
    }
    pub fn split_into_shifts(&self) -> Vec<ShiftRecord> {
        let mut segments: Vec<PunchEntry> = self
            .punches
            .iter()
            .filter(|entry| WORK_TYPES.contains(&entry.pay_type))
            .cloned()
            .collect();
        if segments.is_empty() {
            return Vec::new();
        }
        segments.sort_by_key(|entry| entry.time_in.num_seconds_from_midnight());
        let mut grouped_segments: Vec<Vec<PunchEntry>> = vec![vec![segments[0].clone()]];
        for segment in segments.into_iter().skip(1) {
            let previous = grouped_segments.last().and_then(|group| group.last()).unwrap();
            let gap_minutes = minutes_between(previous.time_out, segment.time_in);
            if gap_minutes >= SPLIT_SHIFT_GAP_MINUTES {
                grouped_segments.push(vec![segment]);
            } else {
                grouped_segments.last_mut().unwrap().push(segment);
            }
        }
        let unpaid_breaks: Vec<PunchEntry> = self
            .punches
            .iter()
            .filter(|entry| UNPAID_BREAK_TYPES.contains(&entry.pay_type))
            .cloned()
            .collect();
        let converted_breaks: Vec<PunchEntry> = self
            .punches
            .iter()
            .filter(|entry| NON_COMPLIANT_BREAK_TYPES.contains(&entry.pay_type))
            .cloned()
            .collect();
        grouped_segments
            .into_iter()
            .map(|work_segments| {
                let shift_start = work_segments.first().unwrap().time_in;
                let shift_end = work_segments.last().unwrap().time_out;
                let shift_unpaid_breaks =
                    unpaid_breaks_within_shift(&unpaid_breaks, &work_segments);
                let shift_converted_breaks =
                    entries_within_shift(&converted_breaks, shift_start, shift_end);
                let mut all_entries = Vec::new();
                all_entries.extend(work_segments.iter().cloned());
                all_entries.extend(shift_unpaid_breaks.iter().cloned());
                all_entries.extend(shift_converted_breaks.iter().cloned());
                all_entries.sort_by_key(|entry| entry.time_in.num_seconds_from_midnight());
                ShiftRecord {
                    date: self.date,
                    weekday: self.weekday,
                    worked_minutes: work_segments.iter().map(|entry| entry.total_minutes).sum(),
                    work_segments,
                    unpaid_breaks: shift_unpaid_breaks,
                    converted_breaks: shift_converted_breaks,
                    all_entries,
                }
            })
            .collect()
    }
}
pub struct ShiftRecord {
    pub date: NaiveDate,
    pub weekday: Weekday,
    pub worked_minutes: u32,
    pub work_segments: Vec<PunchEntry>,
    pub unpaid_breaks: Vec<PunchEntry>,
    pub converted_breaks: Vec<PunchEntry>,
    pub all_entries: Vec<PunchEntry>,
}
impl ShiftRecord {
    pub fn shift_span_label(&self) -> String {
        let start = self.work_segments.first().map(|entry| entry.time_in).unwrap();
        let end = self.work_segments.last().map(|entry| entry.time_out).unwrap();
        format!("{} to {}", format_clock_time(start), format_clock_time(end))
    }
}
pub struct BreakViolation {
    pub employee_name: String,
    pub date: NaiveDate,
    pub weekday: Weekday,
    pub shift_span: String,
    pub worked_minutes: u32,
    pub required_breaks: u32,
    pub actual_breaks: u32,
    pub converted_breaks: u32,
    pub reason: String,
}
pub struct DayMatch<'a> {
    pub employee: &'a EmployeeRecord,
    pub day: &'a EmployeeDay,
}
pub struct ClockIssue {
    pub employee_name: String,
    pub date: NaiveDate,
    pub weekday: Weekday,
    pub punch_time: NaiveTime,
    pub field: ClockField,
    pub message: String,
    pub raw_line: String,
}
pub enum ClockField {
    TimeIn,
    TimeOut,
}
pub enum PayType {
    Regular,
    Unpaid,
    BreakConvertedToPaid,
}
pub struct PunchEntry {
    pub weekday: Weekday,
    pub date: NaiveDate,
    pub time_in: NaiveTime,
    pub time_out: NaiveTime,
    pub time_in_flagged: bool,
    pub time_out_flagged: bool,
    pub total_minutes: u32,
    pub pay_type: PayType,
    pub wage_rate: Option<Decimal>,
    pub regular_hours_minutes: Option<u32>,
    pub regular_wages: Option<Decimal>,
    pub overtime_hours_minutes: Option<u32>,
    pub overtime_wages: Option<Decimal>,
    pub total_wages: Option<Decimal>,
    pub raw_line: String,
}
pub struct EmployeeTotals {
    pub total_minutes: u32,
    pub regular_hours_minutes: Option<u32>,
    pub regular_wages: Option<Decimal>,
    pub overtime_hours_minutes: Option<u32>,
    pub overtime_wages: Option<Decimal>,
    pub total_wages: Option<Decimal>,
}
struct ReportBuilder {
    title: Option<String>,
    location_name: Option<String>,
    period: Option<ReportPeriod>,
    employees: Vec<EmployeeBuilder>,
    grand_totals: Option<EmployeeTotals>,
}
struct EmployeeBuilder {
    name: String,
    days: BTreeMap<NaiveDate, Vec<PunchEntry>>,
    totals: Option<EmployeeTotals>,
}
impl EmployeeBuilder {
    fn new(name: String) -> Self {
        Self {
            name,
            days: BTreeMap::new(),
            totals: None,
        }
    }
    fn add_entry(&mut self, entry: PunchEntry) {
        self.days.entry(entry.date).or_default().push(entry);
    }
    fn finish(self) -> EmployeeRecord {
        let days = self
            .days
            .into_iter()
            .map(|(date, mut punches)| {
                punches.sort_by_key(|entry| entry.time_in.num_seconds_from_midnight());
                EmployeeDay {
                    date,
                    weekday: punches
                        .first()
                        .map(|entry| entry.weekday)
                        .unwrap_or_else(|| date.weekday()),
                    punches,
                }
            })
            .collect();
        EmployeeRecord {
            name: self.name,
            days,
            totals: self.totals,
        }
    }
}
pub fn parse_report_text(text: &str) -> Result<TimePunchReport, ParseError> {
    let mut builder = ReportBuilder::default();
    let mut current_employee: Option<usize> = None;
    let mut expect_location = false;
    for line in iter_clean_lines(text.lines()) {
        if line == "Employee Time Detail" {
            builder.title = Some(line.clone());
            expect_location = true;
            continue;
        }
        if expect_location && !line.starts_with("From ") {
            builder.location_name = Some(line.clone());
            expect_location = false;
            continue;
        }
        expect_location = false;
        if line.starts_with("From ") {
            builder.period = Some(parse_period(&line)?);
            continue;
        }
        if let Some(captures) = employee_totals_regex().captures(&line) {
            if let Some(index) = current_employee {
                let total_minutes = parse_duration_to_minutes(captures.name("total").unwrap().as_str())?;
                let tail = captures.name("tail").map(|m| m.as_str()).unwrap_or("").trim();
                builder.employees[index].totals = Some(parse_totals_tail(total_minutes, tail)?);
            }
            continue;
        }
        if let Some(captures) = grand_totals_regex().captures(&line) {
            let total_minutes = parse_duration_to_minutes(captures.name("total").unwrap().as_str())?;
            let tail = captures.name("tail").map(|m| m.as_str()).unwrap_or("").trim();
            builder.grand_totals = Some(parse_totals_tail(total_minutes, tail)?);
            continue;
        }
        if should_ignore_line(&line) {
            continue;
        }
        if employee_header_regex().is_match(&line) && line.contains(',') {
            current_employee = Some(ensure_employee(&mut builder.employees, line));
            continue;
        }
        if let Some(captures) = punch_line_regex().captures(&line) {
            if let Some(index) = current_employee {
                let entry = build_entry(&line, &captures)?;
                builder.employees[index].add_entry(entry);
            }
        }
    }
    Ok(TimePunchReport {
        title: builder
            .title
            .unwrap_or_else(|| "Employee Time Detail".to_string()),
        location_name: builder.location_name,
        period: builder.period,
        employees: builder
            .employees
            .into_iter()
            .map(EmployeeBuilder::finish)
            .collect(),
        grand_totals: builder.grand_totals,
    })
}
fn ensure_employee(employees: &mut Vec<EmployeeBuilder>, name: String) -> usize {
    if let Some(index) = employees.iter().position(|employee| employee.name == name) {
        return index;
    }
    employees.push(EmployeeBuilder::new(name));
    employees.len() - 1
}
fn build_entry(line: &str, captures: &regex::Captures<'_>) -> Result<PunchEntry, ParseError> {
    let weekday = parse_weekday(captures.name("weekday").unwrap().as_str());
    let date_text = captures.name("date").unwrap().as_str();
    let date = NaiveDate::parse_from_str(date_text, "%m/%d/%Y").map_err(|source| {
        ParseError::InvalidDate {
            value: date_text.to_string(),
            source,
        }
    })?;
    let time_in_text = captures.name("time_in").unwrap().as_str();
    let time_out_text = captures.name("time_out").unwrap().as_str();
    let (time_in, time_in_flagged) = parse_clock_time_with_flag(time_in_text)?;
    let (time_out, time_out_flagged) = parse_clock_time_with_flag(time_out_text)?;
    let pay_type = parse_pay_type(captures.name("pay_type").unwrap().as_str());
    let total_minutes = parse_duration_to_minutes(captures.name("total").unwrap().as_str())?;
    let tail = captures.name("tail").map(|match_| match_.as_str()).unwrap_or("").trim();
    let columns = parse_punch_tail(tail)?;
    Ok(PunchEntry {
        weekday,
        date,
        time_in,
        time_out,
        time_in_flagged,
        time_out_flagged,
        total_minutes,
        pay_type,
        wage_rate: columns.wage_rate,
        regular_hours_minutes: columns.regular_hours_minutes,
        regular_wages: columns.regular_wages,
        overtime_hours_minutes: columns.overtime_hours_minutes,
        overtime_wages: columns.overtime_wages,
        total_wages: columns.total_wages,
        raw_line: line.to_string(),
    })
}
struct ParsedColumns {
    wage_rate: Option<Decimal>,
    regular_hours_minutes: Option<u32>,
    regular_wages: Option<Decimal>,
    overtime_hours_minutes: Option<u32>,
    overtime_wages: Option<Decimal>,
    total_wages: Option<Decimal>,
}
fn parse_punch_tail(tail: &str) -> Result<ParsedColumns, ParseError> {
    if tail.is_empty() {
        return Ok(ParsedColumns::default());
    }
    let mut columns = ParsedColumns::default();
    let mut rest = tail;
    if let Some(token) = tail.split_whitespace().next() {
        if token.starts_with('$') {
            columns.wage_rate = Some(parse_money(token)?);
            rest = tail[token.len()..].trim_start();
        }
    }
    fill_duration_and_wage_columns(rest, columns)
}
fn parse_totals_tail(total_minutes: u32, tail: &str) -> Result<EmployeeTotals, ParseError> {
    let columns = fill_duration_and_wage_columns(tail, ParsedColumns::default())?;
    Ok(EmployeeTotals {
        total_minutes,
        regular_hours_minutes: columns.regular_hours_minutes,
        regular_wages: columns.regular_wages,
        overtime_hours_minutes: columns.overtime_hours_minutes,
        overtime_wages: columns.overtime_wages,
        total_wages: columns.total_wages,
    })
}
fn fill_duration_and_wage_columns(
    text: &str,
    mut columns: ParsedColumns,
) -> Result<ParsedColumns, ParseError> {
    let durations = duration_regex()
        .find_iter(text)
        .map(|match_| parse_duration_to_minutes(match_.as_str()))
        .collect::<Result<Vec<_>, _>>()?;
    let monies = money_regex()
        .find_iter(text)
        .map(|match_| parse_money(match_.as_str()))
        .collect::<Result<Vec<_>, _>>()?;
    columns.regular_hours_minutes = durations.first().copied();
    columns.overtime_hours_minutes = durations.get(1).copied();
    columns.regular_wages = monies.first().copied();
    columns.overtime_wages = if durations.len() > 1 {
        monies.get(1).copied()
    } else {
        None
    };
    columns.total_wages = monies.last().copied();
    Ok(columns)
}
fn parse_period(line: &str) -> Result<ReportPeriod, ParseError> {
    let captures = period_regex().captures(line).unwrap();
    let start_text = captures.name("start").unwrap().as_str();
    let end_text = captures.name("end").unwrap().as_str();
    let start = NaiveDate::parse_from_str(start_text, "%A, %B %d, %Y").map_err(|source| {
        ParseError::InvalidDate {
            value: start_text.to_string(),
            source,
        }
    })?;
    let end = NaiveDate::parse_from_str(end_text, "%A, %B %d, %Y").map_err(|source| {
        ParseError::InvalidDate {
            value: end_text.to_string(),
            source,
        }
    })?;
    Ok(ReportPeriod {
        label: line.to_string(),
        start,
        end,
    })
}
fn parse_weekday(value: &str) -> Weekday {
    match value {
        "Mon" => Weekday::Mon,
        "Tue" => Weekday::Tue,
        "Wed" => Weekday::Wed,
        "Thu" => Weekday::Thu,
        "Fri" => Weekday::Fri,
        "Sat" => Weekday::Sat,
        "Sun" => Weekday::Sun,
        _ => unreachable!("unsupported weekday"),
    }
}
fn parse_pay_type(value: &str) -> PayType {
    let normalized = value.trim().to_ascii_lowercase();
    match normalized.as_str() {
        "regular" => PayType::Regular,
        "unpaid" => PayType::Unpaid,
        "break (conv to paid)" => PayType::BreakConvertedToPaid,
        _ => unreachable!("unsupported pay type"),
    }
}
fn parse_clock_time_with_flag(value: &str) -> Result<(NaiveTime, bool), ParseError> {
    let flagged = value.contains('*');
    let normalized = normalize_meridiem(value.replace('*', "").trim());
    let time = NaiveTime::parse_from_str(&normalized, "%I:%M %p").map_err(|source| {
        ParseError::InvalidTime {
            value: value.to_string(),
            source,
        }
    })?;
    Ok((time, flagged))
}
fn parse_duration_to_minutes(value: &str) -> Result<u32, ParseError> {
    let (hours, minutes) = value.split_once(':').unwrap();
    Ok(hours.parse::<u32>().unwrap() * 60 + minutes.parse::<u32>().unwrap())
}
fn parse_money(value: &str) -> Result<Decimal, ParseError> {
    let normalized = value.trim().trim_start_matches('$').replace(',', "");
    Decimal::from_str_exact(&normalized).map_err(|source| ParseError::InvalidMoney {
        value: value.to_string(),
        source,
    })
}
fn normalize_meridiem(value: &str) -> String {
    let collapsed = value.split_whitespace().collect::<Vec<_>>().join(" ");
    let lower = collapsed.to_ascii_lowercase();
    if let Some(prefix) = lower.strip_suffix('a') {
        return format!("{} AM", prefix.trim());
    }
    if let Some(prefix) = lower.strip_suffix('p') {
        return format!("{} PM", prefix.trim());
    }
    lower.to_ascii_uppercase()
}
pub fn format_clock_time(time: NaiveTime) -> String {
    time.format("%-I:%M %p").to_string()
}
pub fn format_minutes(total_minutes: u32) -> String {
    format!("{}:{:02}", total_minutes / 60, total_minutes % 60)
}
pub fn determine_required_breaks(worked_minutes: u32) -> u32 {
    if worked_minutes >= 13 * 60 {
        3
    } else if worked_minutes >= 10 * 60 {
        2
    } else if worked_minutes >= 6 * 60 {
        1
    } else {
        0
    }
}
fn iter_clean_lines<'a>(
    lines: impl Iterator<Item = &'a str> + 'a,
) -> impl Iterator<Item = String> + 'a {
    lines.filter_map(|line| {
        let cleaned = line.replace('\u{c}', " ");
        let collapsed = cleaned.split_whitespace().collect::<Vec<_>>().join(" ");
        let trimmed = collapsed.trim();
        if trimmed.is_empty() {
            None
        } else {
            Some(trimmed.to_string())
        }
    })
}
fn should_ignore_line(line: &str) -> bool {
    matches!(
        line,
        "Employee"
            | "Date"
            | "Name"
            | "Time In"
            | "Time Out"
            | "Total Time"
            | "Pay Type"
            | "Wage Rate"
            | "Regular Hours Wages Overtime Hours Wages Total Wages"
    ) || line.starts_with("Punch types of")
        || line.starts_with("* - clock-in time or clock-out time")
        || line.contains("Page ")
        || footer_timestamp_regex().is_match(line)
}
fn minutes_between(start: NaiveTime, end: NaiveTime) -> u32 {
    ((end.num_seconds_from_midnight() as i64 - start.num_seconds_from_midnight() as i64) / 60) as u32
}
fn unpaid_breaks_within_shift(
    breaks: &[PunchEntry],
    work_segments: &[PunchEntry],
) -> Vec<PunchEntry> {
    let mut matches = Vec::new();
    for window in work_segments.windows(2) {
        let previous_end = window[0].time_out;
        let next_start = window[1].time_in;
        matches.extend(
            breaks
                .iter()
                .filter(|entry| entry.time_in >= previous_end && entry.time_out <= next_start)
                .cloned(),
        );
    }
    matches
}
fn entries_within_shift(
    entries: &[PunchEntry],
    shift_start: NaiveTime,
    shift_end: NaiveTime,
) -> Vec<PunchEntry> {
    entries
        .iter()
        .filter(|entry| entry.time_in >= shift_start && entry.time_out <= shift_end)
        .cloned()
        .collect()
}
fn employee_header_regex() -> &'static Regex {
    static REGEX: OnceLock<Regex> = OnceLock::new();
    REGEX.get_or_init(|| Regex::new(r"^[A-Za-z][A-Za-z ,.'()/-]+$").unwrap())
}
fn punch_line_regex() -> &'static Regex {
    static REGEX: OnceLock<Regex> = OnceLock::new();
    REGEX.get_or_init(|| {
        Regex::new(
            r"(?i)^\s*(?P<weekday>Mon|Tue|Wed|Thu|Fri|Sat|Sun),\s+(?P<date>\d{2}/\d{2}/\d{4})\s+(?P<time_in>\d{1,2}:\d{2}\s*[ap]\*?)\s+(?P<time_out>\d{1,2}:\d{2}\s*[ap]\*?)\s+(?P<total>\d{1,4}:\d{2})\s+(?P<pay_type>Regular|Unpaid|Break\s+\(Conv\s+To\s+Paid\))(?P<tail>.*)$",
        )
        .unwrap()
    })
}
fn employee_totals_regex() -> &'static Regex {
    static REGEX: OnceLock<Regex> = OnceLock::new();
    REGEX.get_or_init(|| {
        Regex::new(r"^Employee Totals\s+(?P<total>\d{1,4}:\d{2})(?P<tail>.*)$").unwrap()
    })
}
fn grand_totals_regex() -> &'static Regex {
    static REGEX: OnceLock<Regex> = OnceLock::new();
    REGEX.get_or_init(|| {
        Regex::new(r"^All Employees Grand Total\s+(?P<total>\d{1,4}:\d{2})(?P<tail>.*)$")
            .unwrap()
    })
}
fn period_regex() -> &'static Regex {
    static REGEX: OnceLock<Regex> = OnceLock::new();
    REGEX.get_or_init(|| {
        Regex::new(
            r"^From\s+(?P<start>[A-Za-z]+,\s+[A-Za-z]+\s+\d{1,2},\s+\d{4})\s+through\s+(?P<end>[A-Za-z]+,\s+[A-Za-z]+\s+\d{1,2},\s+\d{4})$",
        )
        .unwrap()
    })
}
fn duration_regex() -> &'static Regex {
    static REGEX: OnceLock<Regex> = OnceLock::new();
    REGEX.get_or_init(|| Regex::new(r"\b\d{1,4}:\d{2}\b").unwrap())
}
fn money_regex() -> &'static Regex {
    static REGEX: OnceLock<Regex> = OnceLock::new();
    REGEX.get_or_init(|| Regex::new(r"\$[\d,]+\.\d{2}").unwrap())
}
fn footer_timestamp_regex() -> &'static Regex {
    static REGEX: OnceLock<Regex> = OnceLock::new();
    REGEX.get_or_init(|| Regex::new(r"^\d{2}/\d{2}/\d{4}\s+\d{2}:\d{2}:\d{2}\s+[AP]M").unwrap())
}
mod tests {
    use super::*;
    fn sample_report() -> TimePunchReport {
        let pdf = include_bytes!("../tp.pdf");
        TimePunchReport::parse_pdf_bytes(pdf).unwrap()
    }
    fn parses_report_metadata_and_totals() {
        let report = sample_report();
        assert_eq!(report.title, "Employee Time Detail");
        assert_eq!(report.location_name.as_deref(), Some("13th & Utica FSU"));
        let period = report.period.as_ref().unwrap();
        assert_eq!(period.start, NaiveDate::from_ymd_opt(2026, 5, 11).unwrap());
        assert_eq!(period.end, NaiveDate::from_ymd_opt(2026, 5, 16).unwrap());
        assert_eq!(report.grand_totals.unwrap().total_wages.unwrap().to_string(), "34336.85");
        assert!(report.employees.len() > 50);
    }
    fn finds_employee_days_and_wages() {
        let report = sample_report();
        let employee = report.employee("Angeles Escobar, James M").unwrap();
        assert_eq!(employee.days.len(), 5);
        assert_eq!(employee.worked_minutes(), 2901);
        assert_eq!(employee.total_wages().unwrap().to_string(), "906.06");
        let saturday = employee
            .day(NaiveDate::from_ymd_opt(2026, 5, 16).unwrap())
            .unwrap();
        assert_eq!(saturday.weekday, Weekday::Sat);
        assert_eq!(saturday.worked_minutes(), 530);
        assert_eq!(saturday.unpaid_breaks().len(), 1);
        assert_eq!(saturday.converted_breaks().len(), 0);
    }
    fn supports_weekday_queries() {
        let report = sample_report();
        let mondays = report.days_for_weekday(Weekday::Mon);
        assert!(mondays.iter().any(|day| day.employee.name == "Baker, Ramond Manley (Ray)"));
        assert!(mondays.iter().any(|day| day.employee.name == "Welch, John"));
    }
    fn flags_break_violations_and_converted_breaks() {
        let report = sample_report();
        let baker = report.employee("Baker, Ramond Manley (Ray)").unwrap();
        let violations = baker.break_violations();
        assert_eq!(violations.len(), 6);
        let tuesday = violations
            .iter()
            .find(|violation| violation.date == NaiveDate::from_ymd_opt(2026, 5, 12).unwrap())
            .unwrap();
        assert_eq!(tuesday.required_breaks, 2);
        assert_eq!(tuesday.actual_breaks, 0);
        assert_eq!(tuesday.converted_breaks, 1);
    }
    fn reports_missing_clock_issues() {
        let report = sample_report();
        assert!(report.clock_issues().is_empty());
        assert!(report.employees_with_clock_issues().is_empty());
    }
}
file: /Users/phillipengland/Desktop/src/cfatp/src/main.rs
use std::path::PathBuf;
use cfatp::{
    BreakViolation, ClockIssue, EmployeeDay, EmployeeRecord, EmployeeTotals, PunchEntry,
    ShiftRecord, TimePunchReport, format_clock_time, format_minutes,
};
use chrono::Weekday;
use clap::{Args, Parser, Subcommand, ValueEnum};
use serde::Serialize;
    name = "cfatp",
    version,
    about = "CLI for inspecting time punch PDFs and exporting structured data"
)]
struct Cli {
    command: Command,
}
enum Command {
    Summary(ReportArgs),
    Employees(EmployeesArgs),
    Search(SearchArgs),
    Employee(EmployeeArgs),
    Weekday(WeekdayArgs),
    BreakViolations(BreakViolationsArgs),
    ClockIssues(ClockIssuesArgs),
    Totals(TotalsArgs),
    Raw(RawArgs),
}
struct ReportArgs {
    pdf: PathBuf,
    json: bool,
}
struct EmployeesArgs {
    pdf: PathBuf,
        long,
        value_enum,
        default_value_t = EmployeeSort::Alphabetical,
        help = "Sort employees by alphabetical name, highest worked hours, or highest total wages"
    )]
    sort: EmployeeSort,
    json: bool,
}
struct SearchArgs {
    pdf: PathBuf,
    query: String,
    json: bool,
}
struct EmployeeArgs {
    pdf: PathBuf,
    name: String,
    json: bool,
}
struct WeekdayArgs {
    pdf: PathBuf,
    weekday: CliWeekday,
    employee: Option<String>,
    json: bool,
}
struct BreakViolationsArgs {
    pdf: PathBuf,
    employee: Option<String>,
    json: bool,
}
struct ClockIssuesArgs {
    pdf: PathBuf,
    employee: Option<String>,
    json: bool,
}
struct TotalsArgs {
    pdf: PathBuf,
    employee: Option<String>,
    json: bool,
}
struct RawArgs {
    pdf: PathBuf,
    employee: Option<String>,
    json: bool,
}
enum CliWeekday {
    Mon,
    Tue,
    Wed,
    Thu,
    Fri,
    Sat,
    Sun,
}
enum EmployeeSort {
    Alphabetical,
    Hours,
    Wages,
}
impl From<CliWeekday> for Weekday {
    fn from(value: CliWeekday) -> Self {
        match value {
            CliWeekday::Mon => Weekday::Mon,
            CliWeekday::Tue => Weekday::Tue,
            CliWeekday::Wed => Weekday::Wed,
            CliWeekday::Thu => Weekday::Thu,
            CliWeekday::Fri => Weekday::Fri,
            CliWeekday::Sat => Weekday::Sat,
            CliWeekday::Sun => Weekday::Sun,
        }
    }
}
fn main() {
    if let Err(err) = run() {
        eprintln!("{err}");
        std::process::exit(1);
    }
}
fn run() -> Result<(), Box<dyn std::error::Error>> {
    let cli = Cli::parse();
    match cli.command {
        Command::Summary(args) => {
            let report = load_report(&args.pdf)?;
            if args.json {
                print_json(&SummaryOutput::from_report(&report))?;
            } else {
                print_summary(&report);
            }
        }
        Command::Employees(args) => {
            let report = load_report(&args.pdf)?;
            let employees = sorted_employee_items(&report, args.sort);
            if args.json {
                print_json(&employees)?;
            } else {
                print_employees(&employees, args.sort);
            }
        }
        Command::Search(args) => {
            let report = load_report(&args.pdf)?;
            let matches = report.find_employees(&args.query);
            if args.json {
                let payload = matches
                    .into_iter()
                    .map(EmployeeListItem::from_employee)
                    .collect::<Vec<_>>();
                print_json(&payload)?;
            } else {
                print_employee_search_results(&args.query, &matches);
            }
        }
        Command::Employee(args) => {
            let report = load_report(&args.pdf)?;
            let employee = find_employee_or_err(&report, &args.name)?;
            if args.json {
                print_json(employee)?;
            } else {
                print_employee(employee);
            }
        }
        Command::Weekday(args) => {
            let report = load_report(&args.pdf)?;
            let weekday = Weekday::from(args.weekday);
            let rows = collect_weekday_rows(&report, weekday, args.employee.as_deref())?;
            if args.json {
                print_json(&rows)?;
            } else {
                print_weekday_rows(weekday, &rows);
            }
        }
        Command::BreakViolations(args) => {
            let report = load_report(&args.pdf)?;
            let rows = collect_break_violations(&report, args.employee.as_deref())?;
            if args.json {
                print_json(&rows)?;
            } else {
                print_break_violations(&rows);
            }
        }
        Command::ClockIssues(args) => {
            let report = load_report(&args.pdf)?;
            let rows = collect_clock_issues(&report, args.employee.as_deref())?;
            if args.json {
                print_json(&rows)?;
            } else {
                print_clock_issues(&rows);
            }
        }
        Command::Totals(args) => {
            let report = load_report(&args.pdf)?;
            if args.json {
                match args.employee.as_deref() {
                    Some(name) => {
                        let employee = find_employee_or_err(&report, name)?;
                        print_json(&EmployeeTotalsOutput::from_employee(employee))?;
                    }
                    None => {
                        print_json(&TotalsOutput::from_report(&report))?;
                    }
                }
            } else {
                match args.employee.as_deref() {
                    Some(name) => {
                        let employee = find_employee_or_err(&report, name)?;
                        print_employee_totals(employee);
                    }
                    None => print_report_totals(&report),
                }
            }
        }
        Command::Raw(args) => {
            let report = load_report(&args.pdf)?;
            if let Some(name) = args.employee.as_deref() {
                let employee = find_employee_or_err(&report, name)?;
                print_json(employee)?;
            } else if args.json {
                print_json(&report)?;
            } else {
                print_summary(&report);
            }
        }
    }
    Ok(())
}
fn load_report(path: &PathBuf) -> Result<TimePunchReport, Box<dyn std::error::Error>> {
    Ok(TimePunchReport::parse_file(path)?)
}
fn find_employee_or_err<'a>(
    report: &'a TimePunchReport,
    name: &str,
) -> Result<&'a EmployeeRecord, Box<dyn std::error::Error>> {
    report
        .employee(name)
        .ok_or_else(|| format!("employee not found: {name}").into())
}
fn collect_weekday_rows(
    report: &TimePunchReport,
    weekday: Weekday,
    employee_name: Option<&str>,
) -> Result<Vec<WeekdayRow>, Box<dyn std::error::Error>> {
    let rows = match employee_name {
        Some(name) => {
            let employee = find_employee_or_err(report, name)?;
            employee
                .days_for_weekday(weekday)
                .into_iter()
                .map(|day| WeekdayRow::from_parts(employee, day))
                .collect()
        }
        None => report
            .days_for_weekday(weekday)
            .into_iter()
            .map(|matched| WeekdayRow::from_parts(matched.employee, matched.day))
            .collect(),
    };
    Ok(rows)
}
fn collect_break_violations(
    report: &TimePunchReport,
    employee_name: Option<&str>,
) -> Result<Vec<BreakViolation>, Box<dyn std::error::Error>> {
    Ok(match employee_name {
        Some(name) => find_employee_or_err(report, name)?.break_violations(),
        None => report.break_violations(),
    })
}
fn collect_clock_issues(
    report: &TimePunchReport,
    employee_name: Option<&str>,
) -> Result<Vec<ClockIssue>, Box<dyn std::error::Error>> {
    Ok(match employee_name {
        Some(name) => find_employee_or_err(report, name)?.clock_issues(),
        None => report.clock_issues(),
    })
}
fn print_json<T: Serialize>(value: &T) -> Result<(), Box<dyn std::error::Error>> {
    println!("{}", serde_json::to_string_pretty(value)?);
    Ok(())
}
fn print_summary(report: &TimePunchReport) {
    println!("Report Summary");
    println!("title: {}", report.title);
    println!(
        "location: {}",
        report.location_name.as_deref().unwrap_or("<unknown>")
    );
    if let Some(period) = &report.period {
        println!("period: {} through {}", period.start, period.end);
    }
    println!("employees: {}", report.employees.len());
    println!("break violations: {}", report.break_violations().len());
    println!("clock issues: {}", report.clock_issues().len());
    if let Some(totals) = report.grand_totals {
        println!("grand total minutes: {}", totals.total_minutes);
        println!("grand total hours: {}", format_minutes(totals.total_minutes));
        println!("grand total wages: {}", format_money_opt(totals.total_wages));
    }
}
fn print_employees(employees: &[EmployeeListItem], sort: EmployeeSort) {
    println!("employees: {} | sort: {}", employees.len(), employee_sort_label(sort));
    for employee in employees {
        println!(
            "{} | days={} | worked={} | wages={}",
            employee.name,
            employee.day_count,
            employee.worked_hours,
            employee
                .total_wages
                .as_deref()
                .map(|value| format!("${value}"))
                .unwrap_or_else(|| "<none>".to_string())
        );
    }
}
fn print_employee_search_results(query: &str, matches: &[&EmployeeRecord]) {
    println!("matches for \"{query}\": {}", matches.len());
    for employee in matches {
        println!(
            "{} | days={} | worked={} | wages={}",
            employee.name,
            employee.days.len(),
            format_minutes(employee.worked_minutes()),
            format_money_opt(employee.total_wages())
        );
    }
}
fn print_employee(employee: &EmployeeRecord) {
    println!("Employee");
    println!("name: {}", employee.name);
    println!("days worked: {}", employee.days.len());
    println!("worked hours: {}", format_minutes(employee.worked_minutes()));
    println!("total wages: {}", format_money_opt(employee.total_wages()));
    println!("clock issues: {}", employee.clock_issues().len());
    println!("break violations: {}", employee.break_violations().len());
    println!();
    for day in &employee.days {
        println!(
            "{} {:?} | worked={} | unpaid_breaks={} | converted_breaks={} | span={}",
            day.date,
            day.weekday,
            format_minutes(day.worked_minutes()),
            day.unpaid_breaks().len(),
            day.converted_breaks().len(),
            day.shift_span_label().unwrap_or_else(|| "<none>".to_string())
        );
        for punch in &day.punches {
            println!("  {}", format_punch(punch));
        }
    }
}
fn print_weekday_rows(weekday: Weekday, rows: &[WeekdayRow]) {
    println!("{weekday:?} matches: {}", rows.len());
    for row in rows {
        println!(
            "{} | {} | worked={} | unpaid_breaks={} | converted_breaks={} | span={}",
            row.employee_name,
            row.date,
            format_minutes(row.worked_minutes),
            row.unpaid_breaks,
            row.converted_breaks,
            row.shift_span.as_deref().unwrap_or("<none>")
        );
    }
}
fn print_break_violations(rows: &[BreakViolation]) {
    println!("break violations: {}", rows.len());
    for row in rows {
        println!(
            "{} | {} {:?} | {} | worked={} | required={} | actual={} | converted={}",
            row.employee_name,
            row.date,
            row.weekday,
            row.shift_span,
            format_minutes(row.worked_minutes),
            row.required_breaks,
            row.actual_breaks,
            row.converted_breaks
        );
        println!("  {}", row.reason);
    }
}
fn print_clock_issues(rows: &[ClockIssue]) {
    println!("clock issues: {}", rows.len());
    for row in rows {
        println!(
            "{} | {} {:?} | {:?} {} | {}",
            row.employee_name,
            row.date,
            row.weekday,
            row.field,
            format_clock_time(row.punch_time),
            row.raw_line
        );
    }
}
fn print_report_totals(report: &TimePunchReport) {
    println!("Report Totals");
    if let Some(totals) = report.grand_totals {
        print_totals_block(totals);
    } else {
        println!("no grand totals found");
    }
}
fn print_employee_totals(employee: &EmployeeRecord) {
    println!("Employee Totals");
    println!("name: {}", employee.name);
    match employee.totals {
        Some(totals) => print_totals_block(totals),
        None => println!("no employee totals found"),
    }
}
fn print_totals_block(totals: EmployeeTotals) {
    println!("total minutes: {}", totals.total_minutes);
    println!("total hours: {}", format_minutes(totals.total_minutes));
    println!(
        "regular hours: {}",
        totals
            .regular_hours_minutes
            .map(format_minutes)
            .unwrap_or_else(|| "<none>".to_string())
    );
    println!("regular wages: {}", format_money_opt(totals.regular_wages));
    println!(
        "overtime hours: {}",
        totals
            .overtime_hours_minutes
            .map(format_minutes)
            .unwrap_or_else(|| "<none>".to_string())
    );
    println!("overtime wages: {}", format_money_opt(totals.overtime_wages));
    println!("total wages: {}", format_money_opt(totals.total_wages));
}
fn format_punch(punch: &PunchEntry) -> String {
    let flags = match (punch.time_in_flagged, punch.time_out_flagged) {
        (false, false) => String::new(),
        (true, false) => " [flagged time_in]".to_string(),
        (false, true) => " [flagged time_out]".to_string(),
        (true, true) => " [flagged time_in,time_out]".to_string(),
    };
    format!(
        "{:?} | {} -> {} | total={} | wage_rate={} | total_wages={}{}",
        punch.pay_type,
        format_clock_time(punch.time_in),
        format_clock_time(punch.time_out),
        format_minutes(punch.total_minutes),
        format_money_opt(punch.wage_rate),
        format_money_opt(punch.total_wages),
        flags
    )
}
fn format_money_opt<T: std::fmt::Display>(value: Option<T>) -> String {
    value
        .map(|amount| format!("${amount}"))
        .unwrap_or_else(|| "<none>".to_string())
}
fn sorted_employee_items(report: &TimePunchReport, sort: EmployeeSort) -> Vec<EmployeeListItem> {
    let mut employees = report
        .employees
        .iter()
        .map(EmployeeListItem::from_employee)
        .collect::<Vec<_>>();
    match sort {
        EmployeeSort::Alphabetical => {
            employees.sort_by(|a, b| a.name.cmp(&b.name));
        }
        EmployeeSort::Hours => {
            employees.sort_by(|a, b| {
                b.worked_minutes
                    .cmp(&a.worked_minutes)
                    .then_with(|| a.name.cmp(&b.name))
            });
        }
        EmployeeSort::Wages => {
            employees.sort_by(|a, b| {
                b.total_wages_decimal
                    .cmp(&a.total_wages_decimal)
                    .then_with(|| b.worked_minutes.cmp(&a.worked_minutes))
                    .then_with(|| a.name.cmp(&b.name))
            });
        }
    }
    employees
}
fn employee_sort_label(sort: EmployeeSort) -> &'static str {
    match sort {
        EmployeeSort::Alphabetical => "alphabetical",
        EmployeeSort::Hours => "hours-desc",
        EmployeeSort::Wages => "wages-desc",
    }
}
struct SummaryOutput {
    title: String,
    location_name: Option<String>,
    period_start: Option<String>,
    period_end: Option<String>,
    employee_count: usize,
    break_violation_count: usize,
    clock_issue_count: usize,
    grand_totals: Option<EmployeeTotals>,
}
impl SummaryOutput {
    fn from_report(report: &TimePunchReport) -> Self {
        Self {
            title: report.title.clone(),
            location_name: report.location_name.clone(),
            period_start: report.period.as_ref().map(|period| period.start.to_string()),
            period_end: report.period.as_ref().map(|period| period.end.to_string()),
            employee_count: report.employees.len(),
            break_violation_count: report.break_violations().len(),
            clock_issue_count: report.clock_issues().len(),
            grand_totals: report.grand_totals,
        }
    }
}
struct EmployeeListItem {
    name: String,
    day_count: usize,
    worked_minutes: u32,
    worked_hours: String,
    total_wages: Option<String>,
    total_wages_decimal: Option<rust_decimal::Decimal>,
}
impl EmployeeListItem {
    fn from_employee(employee: &EmployeeRecord) -> Self {
        let total_wages_decimal = employee.total_wages();
        Self {
            name: employee.name.clone(),
            day_count: employee.days.len(),
            worked_minutes: employee.worked_minutes(),
            worked_hours: format_minutes(employee.worked_minutes()),
            total_wages: total_wages_decimal.map(|value| value.to_string()),
            total_wages_decimal,
        }
    }
}
struct WeekdayRow {
    employee_name: String,
    date: String,
    weekday: String,
    worked_minutes: u32,
    worked_hours: String,
    unpaid_breaks: usize,
    converted_breaks: usize,
    shift_span: Option<String>,
}
impl WeekdayRow {
    fn from_parts(employee: &EmployeeRecord, day: &EmployeeDay) -> Self {
        Self {
            employee_name: employee.name.clone(),
            date: day.date.to_string(),
            weekday: format!("{:?}", day.weekday),
            worked_minutes: day.worked_minutes(),
            worked_hours: format_minutes(day.worked_minutes()),
            unpaid_breaks: day.unpaid_breaks().len(),
            converted_breaks: day.converted_breaks().len(),
            shift_span: day.shift_span_label(),
        }
    }
}
struct TotalsOutput {
    grand_totals: Option<EmployeeTotals>,
    employees: Vec<EmployeeTotalsOutput>,
}
impl TotalsOutput {
    fn from_report(report: &TimePunchReport) -> Self {
        Self {
            grand_totals: report.grand_totals,
            employees: report
                .employees
                .iter()
                .map(EmployeeTotalsOutput::from_employee)
                .collect(),
        }
    }
}
struct EmployeeTotalsOutput {
    employee_name: String,
    worked_minutes: u32,
    worked_hours: String,
    totals: Option<EmployeeTotals>,
    shifts: Vec<ShiftSummary>,
}
impl EmployeeTotalsOutput {
    fn from_employee(employee: &EmployeeRecord) -> Self {
        Self {
            employee_name: employee.name.clone(),
            worked_minutes: employee.worked_minutes(),
            worked_hours: format_minutes(employee.worked_minutes()),
            totals: employee.totals,
            shifts: employee
                .shifts()
                .into_iter()
                .map(ShiftSummary::from_shift)
                .collect(),
        }
    }
}
struct ShiftSummary {
    date: String,
    weekday: String,
    span: String,
    worked_minutes: u32,
    worked_hours: String,
    unpaid_breaks: usize,
    converted_breaks: usize,
}
impl ShiftSummary {
    fn from_shift(shift: ShiftRecord) -> Self {
        Self {
            date: shift.date.to_string(),
            weekday: format!("{:?}", shift.weekday),
            span: shift.shift_span_label(),
            worked_minutes: shift.worked_minutes,
            worked_hours: format_minutes(shift.worked_minutes),
            unpaid_breaks: shift.unpaid_breaks.len(),
            converted_breaks: shift.converted_breaks.len(),
        }
    }
}


package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"sort"
	"strconv"
	"strings"
	"time"
)

func parseMoneyCents(value string) int64 {
	normalized := strings.TrimPrefix(strings.ReplaceAll(value, ",", ""), "$")
	parts := strings.SplitN(normalized, ".", 2)
	dollars, _ := strconv.ParseInt(parts[0], 10, 64)
	var cents int64
	if len(parts) == 2 {
		fraction := parts[1]
		if len(fraction) > 2 {
			fraction = fraction[:2]
		}
		for len(fraction) < 2 {
			fraction += "0"
		}
		cents, _ = strconv.ParseInt(fraction, 10, 64)
	}
	return dollars*100 + cents
}

func parseWageCents(value string) (int64, error) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.ReplaceAll(value, ",", ""), "$"))
	if value == "" {
		return 0, errors.New("wage amount is required")
	}
	parts := strings.SplitN(value, ".", 2)
	dollars, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || dollars < 0 {
		return 0, errors.New("wage amount must be a positive dollar amount")
	}
	var cents int64
	if len(parts) == 2 {
		fraction := parts[1]
		if len(fraction) > 2 {
			return 0, errors.New("wage amount can only include cents")
		}
		for len(fraction) < 2 {
			fraction += "0"
		}
		cents, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil || cents < 0 {
			return 0, errors.New("wage amount must be a positive dollar amount")
		}
	}
	total := dollars*100 + cents
	if total <= 0 {
		return 0, errors.New("wage amount must be greater than zero")
	}
	return total, nil
}

func normalizeUSDate(value string) string {
	if t, err := time.Parse("01/02/2006", value); err == nil {
		return t.Format("2006-01-02")
	}
	return value
}

func formatLongDate(value string) string {
	for _, layout := range []string{"Monday, January 2, 2006", "Monday, Jan 2, 2006", "Monday, Jan 02, 2006"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return value
}

func formatISODate(value string) string {
	date, err := time.ParseInLocation("2006-01-02", value, time.Local)
	if err != nil {
		return value
	}
	return date.Format("Monday, January 2, 2006")
}

func calendarDayPath(locationID int64, date string) string {
	return fmt.Sprintf("/locations/%d/calendar/%s", locationID, date)
}

func titleWeekday(value string) string {
	switch strings.ToLower(value) {
	case "mon":
		return "Monday"
	case "tue":
		return "Tuesday"
	case "wed":
		return "Wednesday"
	case "thu":
		return "Thursday"
	case "fri":
		return "Friday"
	case "sat":
		return "Saturday"
	case "sun":
		return "Sunday"
	default:
		return value
	}
}

func normalizeName(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}

func formatHours(minutes int) string {
	return fmt.Sprintf("%.2f", float64(minutes)/60)
}

func formatPercent(part, total int64) string {
	if total == 0 {
		return "0.0%"
	}
	return fmt.Sprintf("%.1f%%", float64(part)*100/float64(total))
}

func formatChartJSON(points any) template.JS {
	data, err := json.Marshal(points)
	if err != nil {
		return "[]"
	}
	return template.JS(data)
}

func formatDollars(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s$%d.%02d", sign, cents/100, cents%100)
}

func weekdayIndex(weekday string) int {
	switch weekday {
	case "Sunday":
		return 0
	case "Monday":
		return 1
	case "Tuesday":
		return 2
	case "Wednesday":
		return 3
	case "Thursday":
		return 4
	case "Friday":
		return 5
	case "Saturday":
		return 6
	default:
		return 7
	}
}

func formatDateList(values map[string]bool) string {
	dates := make([]string, 0, len(values))
	for value := range values {
		dates = append(dates, value)
	}
	sort.Strings(dates)
	return strings.Join(dates, ", ")
}

func cell(row []string, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[idx])
}

func normalizeDate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, layout := range []string{
		"2006-01-02",
		"1/2/2006",
		"01/02/2006",
		"1/2/06",
		"01/02/06",
		"2006/01/02",
		"1-2-2006",
		"01-02-2006",
	} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return value
}

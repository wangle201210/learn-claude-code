package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func validateCron(expr string) error {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return errors.New("cron must have five fields")
	}
	ranges := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	for i, field := range fields {
		if !cronFieldValid(field, ranges[i][0], ranges[i][1]) {
			return fmt.Errorf("invalid cron field %d: %s", i+1, field)
		}
	}
	return nil
}

func cronMatches(expr string, t time.Time) bool {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return false
	}
	return cronFieldMatches(fields[0], t.Minute(), 0, 59, false) &&
		cronFieldMatches(fields[1], t.Hour(), 0, 23, false) &&
		cronFieldMatches(fields[2], t.Day(), 1, 31, false) &&
		cronFieldMatches(fields[3], int(t.Month()), 1, 12, false) &&
		cronFieldMatches(fields[4], int(t.Weekday()), 0, 7, true)
}

func cronFieldValid(field string, min, max int) bool {
	if field == "" {
		return false
	}
	for _, part := range strings.Split(field, ",") {
		if !cronPartValid(part, min, max) {
			return false
		}
	}
	return true
}

func cronPartValid(part string, min, max int) bool {
	if part == "*" {
		return true
	}
	if strings.HasPrefix(part, "*/") {
		n, err := strconv.Atoi(strings.TrimPrefix(part, "*/"))
		return err == nil && n > 0
	}
	if strings.Contains(part, "-") {
		bounds := strings.SplitN(part, "-", 2)
		if len(bounds) != 2 {
			return false
		}
		start, err1 := strconv.Atoi(bounds[0])
		end, err2 := strconv.Atoi(bounds[1])
		return err1 == nil && err2 == nil && start >= min && end <= max && start <= end
	}
	v, err := strconv.Atoi(part)
	return err == nil && v >= min && v <= max
}

func cronFieldMatches(field string, value, min, max int, dow bool) bool {
	for _, part := range strings.Split(field, ",") {
		if cronPartMatches(part, value, min, max, dow) {
			return true
		}
	}
	return false
}

func cronPartMatches(part string, value, min, max int, dow bool) bool {
	if part == "*" {
		return true
	}
	if strings.HasPrefix(part, "*/") {
		n, err := strconv.Atoi(strings.TrimPrefix(part, "*/"))
		return err == nil && n > 0 && value%n == 0
	}
	if strings.Contains(part, "-") {
		bounds := strings.SplitN(part, "-", 2)
		start, err1 := strconv.Atoi(bounds[0])
		end, err2 := strconv.Atoi(bounds[1])
		if err1 != nil || err2 != nil {
			return false
		}
		if dow {
			if start == 7 {
				start = 0
			}
			if end == 7 {
				end = 0
			}
		}
		if start <= end {
			return value >= start && value <= end
		}
		return value >= min && value <= end || value >= start && value <= max
	}
	v, err := strconv.Atoi(part)
	if err != nil {
		return false
	}
	if dow && v == 7 {
		v = 0
	}
	return value == v
}

package smartlist

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var operators = map[string]string{"contains": "LIKE", "not_contains": "NOT LIKE", "equal": "=", "not_equal": "!=", "begins": "BEGINSWITH", "ends": "ENDSWITH", "empty": "IS", "greater": ">", "less": "<", "range": "BETWEEN", "before": "<", "after": ">", "exists": "=", "compatible": "IN"}
var fileTypePattern = regexp.MustCompile(`^\.?[A-Za-z0-9]+$`)
var textEscape = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func integer(value string, maximum int64) (int64, error) {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 || n > maximum || strconv.FormatInt(n, 10) != value {
		return 0, invalid()
	}
	return n, nil
}

const maxNumber int64 = 9007199254740991

func encodeRule(r Rule, loc *time.Location) (storedRule, error) {
	f, ok := findFilter(r.Filter)
	if !ok || !supports(f, r.Condition) || r.Condition != "range" && r.Upper != "" {
		return storedRule{}, invalid()
	}
	saved := storedRule{Col: f.column, Con: operators[r.Condition], Version: ruleVersion}
	var err error
	switch {
	case f.Editor == "text":
		saved.Param, err = encodeText(r, &saved.Con)
	case f.Editor == "date":
		saved.Param, err = encodeDate(r, loc, &saved.Con)
	case f.Editor == "boolean":
		if r.Value != "true" && r.Value != "false" {
			err = invalid()
		} else {
			saved.Param = r.Value
		}
	case f.Editor == "key":
		var key int64
		key, err = integer(r.Value, 23)
		if r.Condition == "compatible" {
			relative := key + 1
			if key%2 != 0 {
				relative = key - 1
			}
			saved.Param = fmt.Sprintf("(%d,%d,%d,%d)", key, relative, (key+22)%24, (key+2)%24)
		} else {
			saved.Param = r.Value
		}
	case f.Editor == "rating":
		var rating int64
		rating, err = integer(r.Value, 5)
		saved.Param = strconv.FormatInt(rating*20, 10)
	case f.Editor == "duration":
		saved.Param, err = encodeDuration(r.Value)
	default:
		max := maxNumber
		if f.ID == "year" {
			max = 9999
		}
		var lower int64
		lower, err = integer(r.Value, max)
		saved.Param = r.Value
		if err == nil && r.Condition == "range" {
			var upper int64
			upper, err = integer(r.Upper, max)
			if upper < lower {
				err = invalid()
			}
			saved.Param += " AND " + r.Upper
		}
	}
	if err != nil {
		return storedRule{}, err
	}
	return saved, nil
}
func encodeText(r Rule, con *string) (string, error) {
	value := textEscape.Replace(r.Value)
	if r.Filter == "file_type" {
		if !fileTypePattern.MatchString(r.Value) {
			return "", invalid()
		}
		*con = "LIKE"
		value = strings.TrimPrefix(value, ".")
	} else {
		switch r.Condition {
		case "contains", "not_contains":
			value = "%" + value + "%"
		case "begins":
			value += "%"
		case "ends":
			value = "%" + value
		case "empty":
			if r.Value != "true" && r.Value != "false" {
				return "", invalid()
			}
			if r.Value == "false" {
				*con = "IS NOT"
			}
		}
	}
	return "'" + value + "'", nil
}
func encodeDate(r Rule, loc *time.Location, con *string) (string, error) {
	day, err := time.ParseInLocation("2006-01-02", r.Value, loc)
	if err != nil || day.Unix() < 0 {
		return "", invalid()
	}
	start, end := day.Unix(), day.AddDate(0, 0, 1).Unix()-1
	switch r.Condition {
	case "equal":
		*con = "BETWEEN"
		return fmt.Sprintf("%d AND %d", start, end), nil
	case "before":
		return strconv.FormatInt(start, 10), nil
	default:
		return strconv.FormatInt(end, 10), nil
	}
}
func encodeDuration(value string) (string, error) {
	if !strings.Contains(value, ":") {
		_, err := integer(value, maxNumber)
		return value, err
	}
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return "", invalid()
	}
	minutes, err := integer(parts[0], maxNumber/60)
	if err != nil {
		return "", err
	}
	seconds, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || len(parts[1]) != 2 || seconds < 0 || seconds > 59 {
		return "", invalid()
	}
	return strconv.FormatInt(minutes*60+seconds, 10), nil
}

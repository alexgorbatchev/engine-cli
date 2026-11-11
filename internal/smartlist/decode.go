package smartlist

import (
	"strconv"
	"strings"
	"time"
)

func decodeRule(saved storedRule, loc *time.Location) (Rule, error) {
	var f Filter
	for _, candidate := range filters {
		if candidate.column == saved.Col {
			f = candidate
			break
		}
	}
	if f.ID == "" {
		return Rule{}, invalid()
	}
	r := Rule{Filter: f.ID, Value: saved.Param}
	for _, c := range f.Conditions {
		if operators[c.ID] == saved.Con {
			r.Condition = c.ID
			break
		}
	}
	switch {
	case f.Editor == "text":
		if len(saved.Param) < 2 || saved.Param[0] != '\'' || saved.Param[len(saved.Param)-1] != '\'' {
			return Rule{}, invalid()
		}
		value := saved.Param[1 : len(saved.Param)-1]
		if f.ID == "file_type" && saved.Con == "LIKE" {
			r.Condition = "equal"
		}
		if saved.Con == "IS NOT" {
			r.Condition = "empty"
		}
		switch r.Condition {
		case "contains", "not_contains":
			if len(value) < 2 || !strings.HasPrefix(value, "%") || !strings.HasSuffix(value, "%") {
				return Rule{}, invalid()
			}
			value = value[1 : len(value)-1]
		case "begins":
			if !strings.HasSuffix(value, "%") {
				return Rule{}, invalid()
			}
			value = strings.TrimSuffix(value, "%")
		case "ends":
			if !strings.HasPrefix(value, "%") {
				return Rule{}, invalid()
			}
			value = strings.TrimPrefix(value, "%")
		}
		var err error
		r.Value, err = unescape(value)
		if err != nil {
			return Rule{}, err
		}
	case f.Editor == "date":
		return decodeDate(saved, f.ID, loc)
	case f.Editor == "key" && saved.Con == "IN":
		if !strings.HasPrefix(saved.Param, "(") {
			return Rule{}, invalid()
		}
		r.Value = strings.Split(strings.TrimPrefix(saved.Param, "("), ",")[0]
	case f.Editor == "rating":
		value, err := integer(saved.Param, 100)
		if err != nil || value%20 != 0 {
			return Rule{}, invalid()
		}
		r.Value = strconv.FormatInt(value/20, 10)
	case r.Condition == "range":
		parts := strings.Split(saved.Param, " AND ")
		if len(parts) != 2 {
			return Rule{}, invalid()
		}
		r.Value, r.Upper = parts[0], parts[1]
	}
	encoded, err := encodeRule(r, loc)
	if err != nil || encoded.Col != saved.Col || encoded.Con != saved.Con || encoded.Param != saved.Param {
		return Rule{}, invalid()
	}
	return r, nil
}
func unescape(value string) (string, error) {
	var result strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == '\\' {
			i++
			if i == len(value) || !strings.ContainsRune(`\%_`, rune(value[i])) {
				return "", invalid()
			}
			c = value[i]
		}
		result.WriteByte(c)
	}
	return result.String(), nil
}
func decodeDate(saved storedRule, id string, loc *time.Location) (Rule, error) {
	parts := strings.Split(saved.Param, " AND ")
	condition := ""
	switch saved.Con {
	case "BETWEEN":
		condition = "equal"
		if len(parts) != 2 {
			return Rule{}, invalid()
		}
	case "<":
		condition = "before"
		if len(parts) != 1 {
			return Rule{}, invalid()
		}
	case ">":
		condition = "after"
		if len(parts) != 1 {
			return Rule{}, invalid()
		}
	default:
		return Rule{}, invalid()
	}
	start, err := integer(parts[0], 253402300799)
	if err != nil {
		return Rule{}, err
	}
	if len(parts) == 2 {
		end, err := integer(parts[1], 253402300799)
		if err != nil || end < start || end-start > 90000 {
			return Rule{}, invalid()
		}
	}
	return Rule{Filter: id, Condition: condition, Value: time.Unix(start, 0).In(loc).Format("2006-01-02")}, nil
}

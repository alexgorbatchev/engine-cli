package smartlist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

// MatchingIDs computes native membership on every read. It never materializes
// PlaylistEntity rows or caches membership. Ordering matches collection reads.
func MatchingIDs(ctx context.Context, d db.Reader, id, libraryFolder string) ([]int64, error) {
	_, raw, err := read(ctx, d, id)
	if err != nil {
		return nil, err
	}
	_, saved, err := parse(raw, "UTC")
	if err != nil {
		return nil, failure("smartlist_rules_unsupported")
	}
	where, args, err := compile(saved, libraryFolder)
	if err != nil {
		return nil, err
	}
	rows, err := d.QueryContext(ctx, "SELECT id FROM Track WHERE "+where+" ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// compile only accepts a document already validated by parse. Stored operators,
// columns and numeric syntax have passed the catalog and exact encoding checks.
func compile(doc storedDocument, libraryFolder string) (string, []any, error) {
	clauses := make([]string, 0, len(doc.Rules))
	var args []any
	for _, rule := range doc.Rules {
		col, op := rule.Col, rule.Con
		switch {
		case op == "IS" || op == "IS NOT":
			clauses = append(clauses, "("+col+" "+op+" NULL)")
			continue
		case col == "stemsStatus":
			files, err := stemFiles(libraryFolder)
			if err != nil {
				return "", nil, err
			}
			col = "CASE WHEN (originTrackId || ' ' || originDatabaseUuid || '.stems') IN (SELECT value FROM json_each(?)) THEN 3 ELSE 0 END"
			args = append(args, files, 0)
			op = "LIKE"
			if rule.Param == "true" {
				op = "NOT LIKE"
			}
			clauses = append(clauses, "("+col+" "+op+" ?)")
			continue
		case col == "isAnalyzed":
			args = append(args, rule.Param == "true")
		case strings.HasPrefix(rule.Param, "'"):
			value := rule.Param[1 : len(rule.Param)-1]
			if op == "BEGINSWITH" || op == "ENDSWITH" {
				op = "LIKE"
			}
			// The recovered compiler emits ESCAPE even for equality. The captured
			// percent-equality case displays no matches. Extend that observed
			// outcome to this invalid native SQL shape without executing it;
			// Desktop's internal error path remains unobserved.
			if strings.Contains(value, `\`) && op != "LIKE" && op != "NOT LIKE" {
				return "0", nil, nil
			}
			args = append(args, value)
			clause := "(" + col + " " + op + " ?"
			if strings.Contains(value, `\`) {
				clause += ` ESCAPE '\'`
			}
			clauses = append(clauses, clause+")")
			continue
		default:
			parts := []string{rule.Param}
			template := "?"
			if op == "BETWEEN" {
				parts = strings.Split(rule.Param, " AND ")
				template = "? AND ?"
			}
			if op == "IN" {
				parts = strings.Split(strings.Trim(rule.Param, "()"), ",")
				template = "(" + strings.TrimSuffix(strings.Repeat("?,", len(parts)), ",") + ")"
			}
			for _, part := range parts {
				n, err := strconv.ParseInt(part, 10, 64)
				if err != nil {
					return "", nil, failure("smartlist_rules_unsupported")
				}
				args = append(args, n)
			}
			clauses = append(clauses, "("+col+" "+op+" "+template+")")
			continue
		}
		clauses = append(clauses, "("+col+" "+op+" ?)")
	}
	join := " AND "
	if doc.Match == "one" {
		join = " OR "
	}
	return strings.Join(clauses, join), args, nil
}

func stemFiles(libraryFolder string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(libraryFolder, "Stems"))
	if errors.Is(err, os.ErrNotExist) {
		return "[]", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading saved stems: %w", err)
	}
	names := []string{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".stems") {
			continue
		}
		info, err := os.Stat(filepath.Join(libraryFolder, "Stems", entry.Name()))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode().IsRegular() {
			names = append(names, entry.Name())
		}
	}
	data, err := json.Marshal(names)
	return string(data), err
}

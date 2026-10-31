package history

import "github.com/alexgorbatchev/engine-cli/internal/db"

type smartlistSuccessorIndex struct {
	tails, previous map[[2]string][]int
}

func indexSmartlistSuccessors(rows []db.SmartlistRecord, bad []bool) smartlistSuccessorIndex {
	index := smartlistSuccessorIndex{tails: make(map[[2]string][]int), previous: make(map[[2]string][]int)}
	for i, row := range rows {
		if bad[i] {
			continue
		}
		if row.NextUUID == "" {
			key := [2]string{row.ParentPath, row.NextPath}
			index.tails[key] = append(index.tails[key], i)
		} else {
			key := [2]string{row.ParentPath, row.NextUUID}
			index.previous[key] = append(index.previous[key], i)
		}
	}
	return index
}

func (index smartlistSuccessorIndex) head(preceding int, rows []db.SmartlistRecord, eligible func(int) bool) (string, bool) {
	target := rows[preceding]
	head := ""
	for _, i := range index.tails[[2]string{target.ParentPath, target.NextPath}] {
		if i == preceding {
			continue
		}
		if !eligible(i) {
			return "", false
		}
		if head != "" {
			return "", false
		}
		head = rows[i].UUID
	}
	seen := make(map[string]bool)
	for head != "" {
		if seen[head] || head == target.UUID {
			return "", false
		}
		seen[head] = true
		previous := index.previous[[2]string{target.ParentPath, head}]
		if len(previous) == 0 {
			return head, true
		}
		if len(previous) != 1 || previous[0] == preceding || !eligible(previous[0]) {
			return "", false
		}
		head = rows[previous[0]].UUID
	}
	return "", true
}

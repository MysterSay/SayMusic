package player

import (
	"fmt"
	"strings"
)

func (a *App) libraryMembership(provider string, ids []string) ([]string, error) {
	if !oneOf(provider, "youtube", "spotify") || len(ids) > 200 {
		return nil, fmt.Errorf("Некоректний запит")
	}
	keys := make([]any, 0, len(ids))
	source := map[string]string{}
	for _, id := range ids {
		key := id
		if provider == "youtube" {
			if !validID.MatchString(id) {
				return nil, fmt.Errorf("Некоректний ID")
			}
		} else {
			if len(id) != 22 || strings.ContainsAny(id, "-_ ") {
				return nil, fmt.Errorf("Некоректний ID")
			}
			for _, c := range id {
				if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
					return nil, fmt.Errorf("Некоректний ID")
				}
			}
			key = "sp_" + id
		}
		if _, ok := source[key]; !ok {
			keys = append(keys, key)
			source[key] = id
		}
	}
	found := []string{}
	if len(keys) == 0 {
		return found, nil
	}
	rows, e := a.db.Query(`SELECT id FROM active_tracks WHERE id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")+`)`, keys...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		found = append(found, source[id])
	}
	return found, rows.Err()
}

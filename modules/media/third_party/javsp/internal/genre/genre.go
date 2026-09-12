package genre

import (
	"encoding/csv"
	"fmt"
	"os"
	"strings"
)

type Map map[string]string

func Load(path string) (Map, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return Map{}, nil
	}

	header := rows[0]
	for i := range header {
		header[i] = strings.TrimPrefix(header[i], "\ufeff")
	}
	idIndex := indexOf(header, "id")
	translateIndex := indexOf(header, "translate")
	if idIndex < 0 || translateIndex < 0 {
		return nil, fmt.Errorf("genre csv requires id and translate columns")
	}

	out := make(Map, len(rows)-1)
	for _, row := range rows[1:] {
		if idIndex >= len(row) || translateIndex >= len(row) {
			continue
		}
		out[row[idIndex]] = row[translateIndex]
	}
	return out, nil
}

func (m Map) Map(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		value, ok := m[id]
		if !ok {
			value = id
		}
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func indexOf(values []string, target string) int {
	for i, value := range values {
		if value == target {
			return i
		}
	}
	return -1
}

// Package selection parses ordered multi-select values used by CLI setup flows.
package selection

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Parse converts comma-separated names or one-based indexes into an ordered,
// deduplicated selection. The special value "all" selects every choice.
func Parse(value string, choices []string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "all" || value == "" {
		return append([]string(nil), choices...), nil
	}

	index := make(map[string]int, len(choices))
	for i, name := range choices {
		index[name] = i
	}
	var selected []string
	seen := make(map[string]bool, len(choices))
	add := func(name string) {
		if !seen[name] {
			selected = append(selected, name)
			seen[name] = true
		}
	}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if i, ok := index[item]; ok {
			add(choices[i])
			continue
		}
		n, err := strconv.Atoi(item)
		if err != nil || n < 1 || n > len(choices) {
			return nil, fmt.Errorf("invalid selection %q", item)
		}
		add(choices[n-1])
	}
	if len(selected) == 0 {
		return nil, errors.New("select at least one item")
	}
	return selected, nil
}

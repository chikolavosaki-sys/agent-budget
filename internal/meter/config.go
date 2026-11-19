package meter

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseBudgets parses a budget list like "default=1000, team-a=500" into a map
// of team name to token limit. An empty string returns an empty map.
func ParseBudgets(s string) (map[string]int64, error) {
	budgets := make(map[string]int64)
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		team, rawLimit, ok := strings.Cut(part, "=")
		team, rawLimit = strings.TrimSpace(team), strings.TrimSpace(rawLimit)
		if !ok || team == "" {
			return nil, fmt.Errorf("budget %q: want team=limit", part)
		}
		limit, err := strconv.ParseInt(rawLimit, 10, 64)
		if err != nil || limit < 0 {
			return nil, fmt.Errorf("budget %q: limit must be a whole number >= 0", part)
		}
		if _, dup := budgets[team]; dup {
			return nil, fmt.Errorf("budget for team %q is set twice", team)
		}
		budgets[team] = limit
	}
	return budgets, nil
}

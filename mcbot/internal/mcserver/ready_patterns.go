package mcserver

import (
	"fmt"
	"regexp"
	"strconv"
)

type readyPattern struct {
	name string
	re   *regexp.Regexp
}

var builtinReadyPatterns = []readyPattern{
	{
		name: "itzg_vanilla_default",
		re:   regexp.MustCompile(`Done \(([0-9.]+)s\)! For help, type "help"`),
	},
	{
		name: "itzg_vanilla_short",
		re:   regexp.MustCompile(`Done \(([0-9.]+)s\)!`),
	},
	{
		name: "paper_spigot_dedicated",
		re:   regexp.MustCompile(`Dedicated server took ([0-9.]+) seconds`),
	},
}

func newReadyMatchers() ([]readyPattern, error) {
	for i, p := range builtinReadyPatterns {
		if p.re == nil {
			return nil, fmt.Errorf("pattern %d (%s) has nil regexp", i, p.name)
		}
		if p.re.NumSubexp() < 1 {
			return nil, fmt.Errorf("pattern %s must have at least 1 capture group for load seconds", p.name)
		}
	}

	return builtinReadyPatterns, nil
}

func parseLoadSeconds(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}

func matchesReadyPattern(logText string, patterns []readyPattern) (bool, float64) {
	for _, pattern := range patterns {
		matches := pattern.re.FindStringSubmatch(logText)
		if len(matches) >= 2 {
			loadSeconds, err := parseLoadSeconds(matches[1])
			if err != nil {
				continue
			}
			return true, loadSeconds
		}
	}
	return false, 0
}

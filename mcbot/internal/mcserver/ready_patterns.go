package mcserver

import (
	"fmt"
	"regexp"
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
	patterns := make([]readyPattern, len(builtinReadyPatterns))
	copy(patterns, builtinReadyPatterns)

	for i, p := range patterns {
		if p.re == nil {
			return nil, fmt.Errorf("pattern %d (%s) has nil regexp", i, p.name)
		}
		if p.re.NumSubexp() < 1 {
			return nil, fmt.Errorf("pattern %s must have at least 1 capture group for load seconds", p.name)
		}
	}

	return patterns, nil
}

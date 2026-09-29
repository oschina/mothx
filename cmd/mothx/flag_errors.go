package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func installFriendlyFlagErrors(cmd *cobra.Command) {
	cmd.SilenceUsage = true
	cmd.SetFlagErrorFunc(friendlyFlagError)
	for _, child := range cmd.Commands() {
		installFriendlyFlagErrors(child)
	}
}

func friendlyFlagError(cmd *cobra.Command, err error) error {
	if err == nil || errors.Is(err, pflag.ErrHelp) {
		return err
	}

	name, shorthand, ok := parseUnknownFlag(err.Error())
	if !ok {
		return fmt.Errorf("invalid argument: %w", err)
	}

	if suggestion := suggestFlag(cmd, name, shorthand); suggestion != "" {
		return fmt.Errorf("invalid argument: %s\n\nDid you mean %s?\nRun '%s --help' to see all commands.", err, suggestion, helpCommandName(cmd))
	}
	return fmt.Errorf("invalid argument: %s\n\nRun '%s --help' to see all commands.", err, helpCommandName(cmd))
}

func parseUnknownFlag(message string) (name string, shorthand bool, ok bool) {
	const longPrefix = "unknown flag: --"
	if strings.HasPrefix(message, longPrefix) {
		name := strings.TrimSpace(strings.TrimPrefix(message, longPrefix))
		name = strings.TrimLeft(name, "-")
		if before, _, found := strings.Cut(name, "="); found {
			name = before
		}
		return name, false, name != ""
	}

	const shortPrefix = "unknown shorthand flag: "
	if strings.HasPrefix(message, shortPrefix) {
		rest := strings.TrimPrefix(message, shortPrefix)
		first := strings.IndexByte(rest, '\'')
		if first < 0 {
			return "", false, false
		}
		rest = rest[first+1:]
		second := strings.IndexByte(rest, '\'')
		if second <= 0 {
			return "", false, false
		}
		return rest[:second], true, true
	}

	return "", false, false
}

func suggestFlag(cmd *cobra.Command, name string, shorthand bool) string {
	candidates := collectFlagCandidates(cmd, name, shorthand)
	if len(candidates) == 0 {
		return ""
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			if lengthDistance(name, candidates[i].name) != lengthDistance(name, candidates[j].name) {
				return lengthDistance(name, candidates[i].name) < lengthDistance(name, candidates[j].name)
			}
			return candidates[i].display < candidates[j].display
		}
		return candidates[i].score < candidates[j].score
	})

	best := candidates[0]
	if !isUsefulFlagSuggestion(name, best.name, best.score, shorthand) {
		return ""
	}
	return best.display
}

func validateRootArgs(cmd *cobra.Command, args []string, flags *cliFlags) error {
	if len(args) == 0 || flags.print || flags.initServe || flags.initA2AMaster || flags.listSessions {
		return nil
	}

	if suggestion := suggestSubcommand(cmd, args); suggestion != "" {
		return fmt.Errorf("invalid argument: unknown command %q for %q\n\nDid you mean %s?\nRun '%s --help' to see all commands.", args[0], cmd.CommandPath(), suggestion, helpCommandName(cmd))
	}
	return fmt.Errorf("invalid argument: unknown command %q for %q\n\nRun '%s --help' to see all commands.", args[0], cmd.CommandPath(), helpCommandName(cmd))
}

func helpCommandName(cmd *cobra.Command) string {
	if cmd == nil {
		return "mothx"
	}
	root := cmd.Root()
	if root != nil && root.Name() != "" {
		return root.Name()
	}
	return "mothx"
}

func suggestSubcommand(cmd *cobra.Command, args []string) string {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return ""
	}

	name := args[0]
	var candidates []flagCandidate
	for _, child := range cmd.Commands() {
		if child.Hidden || child.Deprecated != "" {
			continue
		}
		candidates = append(candidates, flagCandidate{
			name:    child.Name(),
			display: child.Name(),
			score:   flagDistance(name, child.Name()),
		})
		for _, alias := range child.Aliases {
			candidates = append(candidates, flagCandidate{
				name:    alias,
				display: child.Name(),
				score:   flagDistance(name, alias),
			})
		}
	}
	if len(candidates) == 0 {
		return ""
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			if lengthDistance(name, candidates[i].name) != lengthDistance(name, candidates[j].name) {
				return lengthDistance(name, candidates[i].name) < lengthDistance(name, candidates[j].name)
			}
			return candidates[i].display < candidates[j].display
		}
		return candidates[i].score < candidates[j].score
	})

	best := candidates[0]
	if !isUsefulFlagSuggestion(name, best.name, best.score, false) {
		return ""
	}
	return best.display
}

type flagCandidate struct {
	name    string
	display string
	score   int
}

func collectFlagCandidates(cmd *cobra.Command, name string, shorthand bool) []flagCandidate {
	seen := map[string]bool{}
	var candidates []flagCandidate
	addSet := func(fs *pflag.FlagSet) {
		if fs == nil {
			return
		}
		fs.VisitAll(func(flag *pflag.Flag) {
			if flag.Hidden || flag.Deprecated != "" {
				return
			}
			if shorthand {
				if flag.Shorthand == "" {
					return
				}
				display := "-" + flag.Shorthand
				if flag.Name != "" {
					display += ", --" + flag.Name
				}
				key := "short:" + flag.Shorthand
				if !seen[key] {
					seen[key] = true
					candidates = append(candidates, flagCandidate{name: flag.Shorthand, display: display})
				}
				return
			}
			if flag.Name == "" {
				return
			}
			key := "long:" + flag.Name
			if !seen[key] {
				seen[key] = true
				candidates = append(candidates, flagCandidate{name: flag.Name, display: "--" + flag.Name})
			}
		})
	}

	addSet(cmd.LocalNonPersistentFlags())
	addSet(cmd.PersistentFlags())
	addSet(cmd.InheritedFlags())

	for i := range candidates {
		candidates[i].score = flagDistance(name, candidates[i].name)
	}
	return candidates
}

func isUsefulFlagSuggestion(input, candidate string, score int, shorthand bool) bool {
	if shorthand {
		return score == 1
	}
	if strings.HasPrefix(candidate, input) || strings.HasPrefix(input, candidate) {
		return abs(len(candidate)-len(input)) <= 4
	}
	return score <= 2
}

func flagDistance(a, b string) int {
	a = strings.ToLower(strings.ReplaceAll(a, "-", ""))
	b = strings.ToLower(strings.ReplaceAll(b, "-", ""))
	if a == b {
		return 0
	}
	if adjacentTransposition(a, b) {
		return 1
	}
	if a == "" {
		return len(b)
	}
	if b == "" {
		return len(a)
	}

	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr := make([]int, len(b)+1)
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			curr[j] = min3(
				curr[j-1]+1,
				prev[j]+1,
				prev[j-1]+cost,
			)
		}
		prev = curr
	}
	return prev[len(b)]
}

func adjacentTransposition(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a)-1; i++ {
		if a[i] == b[i] {
			continue
		}
		if a[i] == b[i+1] && a[i+1] == b[i] && a[i+2:] == b[i+2:] {
			return true
		}
		return false
	}
	return false
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func lengthDistance(a, b string) int {
	return abs(len(a) - len(b))
}

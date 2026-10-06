package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// runComplete prints bash completions for COMP_LINE, one per line.
func runComplete() {
	for _, c := range completeLine(os.Getenv("COMP_LINE")) {
		fmt.Println(c)
	}
}

// completeLine splits a command line into completed words and the word being typed.
func completeLine(line string) []string {
	trailing := strings.HasSuffix(line, " ")
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return completeWords(nil, "")
	}
	rest := fields[1:]
	current := ""
	if !trailing && len(rest) > 0 {
		current = rest[len(rest)-1]
		rest = rest[:len(rest)-1]
	}
	return completeWords(rest, current)
}

// completeWords returns completions for current after prev: subcommands and tool names, then
// a tool's unused flags, agent ids after --as-agent, or enum values after an enum flag.
// Completion offers nothing beyond agents and help when Hath cannot be reached.
func completeWords(prev []string, current string) []string {
	tools, err := fetchTools()
	if err != nil {
		return filterPrefix([]string{"agents", "help"}, current)
	}
	names := make([]string, 0, len(tools)+2)
	names = append(names, "agents", "help")
	for _, t := range tools {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	if len(prev) == 0 {
		return filterPrefix(names, current)
	}
	if prev[0] == "help" {
		if len(prev) == 1 {
			toolNames := make([]string, 0, len(tools))
			for _, t := range tools {
				toolNames = append(toolNames, t.Name)
			}
			sort.Strings(toolNames)
			return filterPrefix(toolNames, current)
		}
		return nil
	}
	if prev[0] == "agents" {
		return nil
	}
	detail, err := fetchTool(prev[0])
	if err != nil {
		return nil
	}
	flags, enums := schemaFlags(detail.InputSchema)
	flags = append([]string{"--as-agent", "--as-router"}, flags...)
	sort.Strings(flags)
	if len(prev) > 1 {
		last := prev[len(prev)-1]
		if strings.HasPrefix(last, "--") {
			key := strings.TrimPrefix(last, "--")
			if key == "as-agent" || key == "as_agent" {
				return filterPrefix(agentIDs(), current)
			}
			if vals, ok := enums[key]; ok {
				return filterPrefix(vals, current)
			}
		}
	}
	used := map[string]struct{}{}
	for _, p := range prev[1:] {
		if strings.HasPrefix(p, "--") {
			used[strings.TrimPrefix(strings.SplitN(p, "=", 2)[0], "--")] = struct{}{}
		}
	}
	var remaining []string
	for _, f := range flags {
		key := strings.TrimPrefix(f, "--")
		if _, ok := used[key]; ok {
			continue
		}
		remaining = append(remaining, f)
	}
	if strings.HasPrefix(current, "-") || current == "" {
		return filterPrefix(remaining, current)
	}
	return nil
}

// agentIDs returns Hath's agent ids, sorted, or nil when Hath cannot be reached.
func agentIDs() []string {
	agents, err := fetchAgents()
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(agents))
	for _, agent := range agents {
		ids = append(ids, agent.ID)
	}
	sort.Strings(ids)
	return ids
}

// schemaFlags returns --<property> flags for a tool input schema and the enum values per property.
func schemaFlags(schema map[string]any) (flags []string, enums map[string][]string) {
	enums = map[string][]string{}
	if schema == nil {
		return nil, enums
	}
	props, _ := schema["properties"].(map[string]any)
	for key, raw := range props {
		flags = append(flags, "--"+key)
		prop, _ := raw.(map[string]any)
		if ev, ok := prop["enum"].([]any); ok {
			var vals []string
			for _, x := range ev {
				vals = append(vals, fmt.Sprint(x))
			}
			enums[key] = vals
		}
	}
	sort.Strings(flags)
	return flags, enums
}

// filterPrefix returns the options that start with prefix.
func filterPrefix(options []string, prefix string) []string {
	if prefix == "" {
		return options
	}
	out := make([]string, 0, len(options))
	for _, o := range options {
		if strings.HasPrefix(o, prefix) {
			out = append(out, o)
		}
	}
	return out
}

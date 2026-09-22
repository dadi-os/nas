package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// dimaagBase is the Dimaag origin used by API calls. main sets it from DIMAAG_URL; tests may override.
var dimaagBase string

var httpClient = &http.Client{Timeout: 120 * time.Second}

type toolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type toolsList struct {
	Tools []toolInfo `json:"tools"`
}

type agentInfo struct {
	ID string `json:"id"`
	// Name is the display alias of id (Dimaag keeps both equal).
	Name          string  `json:"name"`
	ParentAgentID *string `json:"parent_agent_id"`
	Active        bool    `json:"active"`
}

type agentsList struct {
	Agents []agentInfo `json:"agents"`
}

type execResult struct {
	OK      bool `json:"ok"`
	Content any  `json:"content"`
	IsError bool `json:"is_error"`
}

type apiError struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func main() {
	base, err := loadDimaagBase()
	if err != nil {
		fmt.Fprintln(os.Stderr, "dadi:", err)
		os.Exit(1)
	}
	dimaagBase = base
	if os.Getenv("COMP_LINE") != "" {
		runComplete()
		return
	}
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "__complete" {
		runComplete()
		return
	}
	if err := run(args); err != nil {
		fmt.Fprintln(os.Stderr, "dadi:", err)
		os.Exit(1)
	}
}

// loadDimaagBase reads DIMAAG_URL (required, no default).
func loadDimaagBase() (string, error) {
	u := strings.TrimSpace(os.Getenv("DIMAAG_URL"))
	if u == "" {
		return "", fmt.Errorf("DIMAAG_URL is required")
	}
	return strings.TrimRight(u, "/"), nil
}

func run(args []string) error {
	if len(args) > 0 && args[0] == "agents" {
		return cmdAgents()
	}
	tool, help, input, err := parseArgs(args)
	if err != nil {
		return err
	}
	if help && tool == "" {
		return cmdHelp("")
	}
	if help {
		return cmdHelp(tool)
	}
	return cmdExecute(tool, input)
}

func parseArgs(args []string) (tool string, help bool, input map[string]any, err error) {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		name := ""
		if len(args) > 1 && args[0] == "help" {
			name = args[1]
		}
		return name, true, nil, nil
	}
	tool = args[0]
	rest := args[1:]
	for _, a := range rest {
		if a == "help" || a == "--help" || a == "-h" {
			return tool, true, nil, nil
		}
	}
	input, err = parseFlags(rest)
	return tool, false, input, err
}

func cmdHelp(name string) error {
	if name == "" {
		list, err := fetchTools()
		if err != nil {
			return err
		}
		fmt.Println("dadi <tool> (--as-agent-id <id> | --as-dadi | --as-user) [--key value …]")
		fmt.Println("dadi agents")
		fmt.Println("dadi help [tool]")
		fmt.Println()
		fmt.Println("Execute requires one caller identity: --as-agent-id <kebab-id>, --as-dadi, or --as-user.")
		fmt.Println()
		for _, t := range list {
			fmt.Printf("  %s\n    %s\n", t.Name, t.Description)
		}
		return nil
	}
	detail, err := fetchTool(name)
	if err != nil {
		return err
	}
	fmt.Println(detail.Name)
	fmt.Println(detail.Description)
	fmt.Println("Parameters:")
	fmt.Println("  --as-agent-id (string) — kebab-case agent id that holds the grant (see dadi agents)")
	fmt.Println("  --as-dadi — act as Dadi (router authority tools only)")
	fmt.Println("  --as-user — act as the human (any tool, no grant check)")
	fmt.Println("  Exactly one of --as-agent-id, --as-dadi, or --as-user is required.")
	fmt.Print(formatSchema(detail.InputSchema))
	return nil
}

func cmdExecute(name string, input map[string]any) error {
	if input == nil {
		input = map[string]any{}
	}
	asAgentID, err := takeCallerIdentity(input)
	if err != nil {
		return err
	}
	input["as_agent_id"] = asAgentID
	raw, err := api(http.MethodPost, "/tools/"+urlPath(name)+"/execute", input)
	if err != nil {
		return err
	}
	var result execResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("bad execute payload: %w", err)
	}
	rendered := renderContent(result.Content)
	if result.IsError {
		return fmt.Errorf("%s", rendered)
	}
	fmt.Println(rendered)
	return nil
}

// takeCallerIdentity removes identity flags from the flag map and returns as_agent_id for Dimaag.
func takeCallerIdentity(input map[string]any) (string, error) {
	asAgentID, hasAsAgentID, err := takeStringFlag(input, "as_agent_id", "as-agent-id")
	if err != nil {
		return "", err
	}
	hasAsDadi, err := takeBoolFlag(input, "as_dadi", "as-dadi")
	if err != nil {
		return "", err
	}
	hasAsUser, err := takeBoolFlag(input, "as_user", "as-user")
	if err != nil {
		return "", err
	}

	n := 0
	if hasAsAgentID {
		n++
	}
	if hasAsDadi {
		n++
	}
	if hasAsUser {
		n++
	}
	if n > 1 {
		return "", fmt.Errorf("pass only one of --as-agent-id, --as-dadi, and --as-user")
	}
	if n == 0 {
		return "", fmt.Errorf("one of --as-agent-id <kebab-id>, --as-dadi, or --as-user is required")
	}
	if hasAsDadi {
		return "dadi", nil
	}
	if hasAsUser {
		return "user", nil
	}
	return asAgentID, nil
}

func takeStringFlag(input map[string]any, keys ...string) (value string, ok bool, err error) {
	for _, key := range keys {
		raw, present := input[key]
		if !present {
			continue
		}
		delete(input, key)
		s, isString := raw.(string)
		if !isString || strings.TrimSpace(s) == "" {
			return "", true, fmt.Errorf("--%s requires a value", strings.ReplaceAll(key, "_", "-"))
		}
		return strings.TrimSpace(s), true, nil
	}
	return "", false, nil
}

func takeBoolFlag(input map[string]any, keys ...string) (bool, error) {
	for _, key := range keys {
		raw, present := input[key]
		if !present {
			continue
		}
		delete(input, key)
		switch v := raw.(type) {
		case bool:
			if !v {
				return false, fmt.Errorf("--%s does not take a value", strings.ReplaceAll(key, "_", "-"))
			}
			return true, nil
		default:
			return false, fmt.Errorf("--%s does not take a value", strings.ReplaceAll(key, "_", "-"))
		}
	}
	return false, nil
}

func cmdAgents() error {
	agents, err := fetchAgents()
	if err != nil {
		return err
	}
	printAgentForest(agents)
	return nil
}

func printAgentForest(agents []agentInfo) {
	byParent := map[string][]agentInfo{}
	var roots []agentInfo
	for _, agent := range agents {
		if agent.ParentAgentID == nil {
			roots = append(roots, agent)
			continue
		}
		parent := *agent.ParentAgentID
		byParent[parent] = append(byParent[parent], agent)
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].ID < roots[j].ID })
	for _, root := range roots {
		printAgentLine(root, 0)
		printAgentChildren(root.ID, byParent, 1)
	}
}

func printAgentChildren(parentID string, byParent map[string][]agentInfo, depth int) {
	children := byParent[parentID]
	sort.Slice(children, func(i, j int) bool { return children[i].ID < children[j].ID })
	for _, child := range children {
		printAgentLine(child, depth)
		printAgentChildren(child.ID, byParent, depth+1)
	}
}

func printAgentLine(agent agentInfo, depth int) {
	state := "dormant"
	if agent.Active {
		state = "active"
	}
	indent := strings.Repeat("  ", depth)
	fmt.Printf("%s%s  %s\n", indent, agent.ID, state)
}

func renderContent(content any) string {
	switch v := content.(type) {
	case string:
		var parsed any
		if json.Unmarshal([]byte(v), &parsed) == nil {
			b, err := json.MarshalIndent(parsed, "", "  ")
			if err == nil {
				return string(b)
			}
		}
		return v
	default:
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(b)
	}
}

func fetchTools() ([]toolInfo, error) {
	raw, err := api(http.MethodGet, "/tools", nil)
	if err != nil {
		return nil, err
	}
	var wrap toolsList
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, fmt.Errorf("bad tools payload: %w", err)
	}
	if wrap.Tools == nil {
		return nil, fmt.Errorf("bad tools payload")
	}
	return wrap.Tools, nil
}

func fetchTool(name string) (toolInfo, error) {
	raw, err := api(http.MethodGet, "/tools/"+urlPath(name), nil)
	if err != nil {
		return toolInfo{}, err
	}
	var detail toolInfo
	if err := json.Unmarshal(raw, &detail); err != nil {
		return toolInfo{}, fmt.Errorf("bad tool payload: %w", err)
	}
	if detail.Name == "" {
		return toolInfo{}, fmt.Errorf("bad tool payload")
	}
	return detail, nil
}

func fetchAgents() ([]agentInfo, error) {
	raw, err := api(http.MethodGet, "/agents", nil)
	if err != nil {
		return nil, err
	}
	var wrap agentsList
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, fmt.Errorf("bad agents payload: %w", err)
	}
	if wrap.Agents == nil {
		return nil, fmt.Errorf("bad agents payload")
	}
	return wrap.Agents, nil
}

func urlPath(name string) string {
	return url.PathEscape(name)
}

func api(method, path string, payload any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, dimaagBase+path, body)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		var ae apiError
		if json.Unmarshal(data, &ae) == nil && ae.Error.Type != "" {
			if ae.Error.Message != "" {
				return nil, fmt.Errorf("%s: %s", ae.Error.Type, ae.Error.Message)
			}
			return nil, fmt.Errorf("%s", ae.Error.Type)
		}
		return nil, fmt.Errorf("dimaag %s", strings.TrimSpace(resp.Status))
	}
	return data, nil
}

func parseFlags(args []string) (map[string]any, error) {
	out := map[string]any{}
	for i := 0; i < len(args); i++ {
		token := args[i]
		if !strings.HasPrefix(token, "--") {
			return nil, fmt.Errorf("unexpected argument %s (use --key value)", token)
		}
		body := token[2:]
		if body == "" {
			return nil, fmt.Errorf("empty flag")
		}
		if eq := strings.IndexByte(body, '='); eq >= 0 {
			out[body[:eq]] = coerce(body[eq+1:])
			continue
		}
		if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
			out[body] = true
			continue
		}
		out[body] = coerce(args[i+1])
		i++
	}
	return out, nil
}

func coerce(value string) any {
	switch value {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	trimmed := strings.TrimSpace(value)
	if len(trimmed) > 0 && (trimmed[0] == '[' || trimmed[0] == '{') {
		var decoded any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
			return decoded
		}
	}
	if n, err := strconv.ParseInt(value, 10, 64); err == nil {
		return n
	}
	if n, err := strconv.ParseFloat(value, 64); err == nil {
		return n
	}
	return value
}

func formatSchema(schema map[string]any) string {
	if schema == nil {
		return "  (no parameters)\n"
	}
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return "  (no parameters)\n"
	}
	reqSet := map[string]struct{}{}
	if req, ok := schema["required"].([]any); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				reqSet[s] = struct{}{}
			}
		}
	}
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		prop, _ := props[key].(map[string]any)
		typ := "any"
		switch t := prop["type"].(type) {
		case string:
			typ = t
		case []any:
			parts := make([]string, 0, len(t))
			for _, x := range t {
				parts = append(parts, fmt.Sprint(x))
			}
			typ = strings.Join(parts, "|")
		}
		req := "optional"
		if _, ok := reqSet[key]; ok {
			req = "required"
		}
		desc := ""
		if d, ok := prop["description"].(string); ok && d != "" {
			desc = " — " + d
		}
		fmt.Fprintf(&b, "  --%s (%s, %s)%s\n", key, typ, req, desc)
	}
	return b.String()
}

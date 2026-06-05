package mcptools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	einomcp "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/wangle201210/learn-claude-code/final_eino_adk/internal/workspace"
)

type mcpServerConfig struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	URL     string            `json:"url,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     []string          `json:"env,omitempty"`
	Tools   []string          `json:"tools,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type rawMCPServerConfig struct {
	Name         string            `json:"name,omitempty"`
	Type         string            `json:"type,omitempty"`
	Transport    string            `json:"transport,omitempty"`
	URL          string            `json:"url,omitempty"`
	Command      string            `json:"command,omitempty"`
	Args         []string          `json:"args,omitempty"`
	Env          json.RawMessage   `json:"env,omitempty"`
	Tools        []string          `json:"tools,omitempty"`
	ToolNameList []string          `json:"toolNameList,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Disabled     bool              `json:"disabled,omitempty"`
}

func Load(ctx context.Context) ([]tool.BaseTool, error) {
	configs, err := readMCPConfigs()
	if err != nil {
		return nil, err
	}
	var out []tool.BaseTool
	for _, cfg := range configs {
		tools, err := connectMCPServer(ctx, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mcp %s: %v\n", cfg.Name, err)
			continue
		}
		out = append(out, tools...)
	}
	return out, nil
}

func readMCPConfigs() ([]mcpServerConfig, error) {
	return readMCPConfigsFrom(workspace.Dir())
}

func readMCPConfigsFrom(root string) ([]mcpServerConfig, error) {
	if raw := strings.TrimSpace(os.Getenv("FINAL_EINO_MCP_SERVERS")); raw != "" {
		return parseMCPConfigBytes([]byte(raw), "FINAL_EINO_MCP_SERVERS", true)
	}

	merged := newMCPConfigMerger()
	for _, path := range []string{
		filepath.Join(root, ".mcp.json"),
		filepath.Join(root, ".claude", "settings.json"),
		filepath.Join(root, ".claude", "settings.local.json"),
	} {
		configs, err := readMCPConfigFile(path, filepath.Base(path) == ".mcp.json")
		if err != nil {
			return nil, err
		}
		merged.upsert(configs...)
	}

	for _, url := range splitEnvList(os.Getenv("FINAL_EINO_MCP_SSE_URLS")) {
		merged.upsert(mcpServerConfig{
			Name: "sse:" + url,
			Type: "sse",
			URL:  url,
		})
	}
	for _, url := range splitEnvList(os.Getenv("FINAL_EINO_MCP_HTTP_URLS")) {
		merged.upsert(mcpServerConfig{
			Name: "http:" + url,
			Type: "http",
			URL:  url,
		})
	}
	if raw := strings.TrimSpace(os.Getenv("FINAL_EINO_MCP_STDIO")); raw != "" {
		fields := strings.Fields(raw)
		if len(fields) > 0 {
			merged.upsert(mcpServerConfig{
				Name:    "stdio:" + fields[0],
				Type:    "stdio",
				Command: fields[0],
				Args:    fields[1:],
				Env:     os.Environ(),
			})
		}
	}
	return merged.slice(), nil
}

func connectMCPServer(ctx context.Context, cfg mcpServerConfig) ([]tool.BaseTool, error) {
	typ := strings.ToLower(strings.TrimSpace(cfg.Type))
	if typ == "" && cfg.Command != "" {
		typ = "stdio"
	}
	if typ == "" {
		typ = "sse"
	}

	var cli client.MCPClient
	var err error
	switch typ {
	case "sse":
		if cfg.URL == "" {
			return nil, fmt.Errorf("url is required")
		}
		cli, err = client.NewSSEMCPClient(cfg.URL, client.WithHeaders(cfg.Headers))
	case "http", "streamable_http":
		if cfg.URL == "" {
			return nil, fmt.Errorf("url is required")
		}
		cli, err = client.NewStreamableHttpClient(cfg.URL, transport.WithHTTPHeaders(cfg.Headers))
	case "stdio":
		if cfg.Command == "" {
			return nil, fmt.Errorf("command is required")
		}
		env := cfg.Env
		if len(env) == 0 {
			env = os.Environ()
		}
		cli, err = client.NewStdioMCPClient(cfg.Command, env, cfg.Args...)
	default:
		return nil, fmt.Errorf("unsupported type %q", cfg.Type)
	}
	if err != nil {
		return nil, err
	}

	if starter, ok := cli.(interface{ Start(context.Context) error }); ok {
		if err := starter.Start(ctx); err != nil {
			_ = cli.Close()
			return nil, err
		}
	}

	req := mcpsdk.InitializeRequest{}
	req.Params.ProtocolVersion = mcpsdk.LATEST_PROTOCOL_VERSION
	req.Params.ClientInfo = mcpsdk.Implementation{
		Name:    "final_eino_adk",
		Version: "0.1.0",
	}
	if _, err := cli.Initialize(ctx, req); err != nil {
		_ = cli.Close()
		return nil, err
	}

	return einomcp.GetTools(ctx, &einomcp.Config{
		Cli:           cli,
		ToolNameList:  cfg.Tools,
		CustomHeaders: cfg.Headers,
	})
}

type mcpConfigMerger struct {
	order []string
	byKey map[string]mcpServerConfig
}

func newMCPConfigMerger() *mcpConfigMerger {
	return &mcpConfigMerger{byKey: map[string]mcpServerConfig{}}
}

func (m *mcpConfigMerger) upsert(configs ...mcpServerConfig) {
	for _, cfg := range configs {
		key := cfg.Name
		if key == "" {
			key = defaultMCPServerName(cfg)
			cfg.Name = key
		}
		if _, ok := m.byKey[key]; !ok {
			m.order = append(m.order, key)
		}
		m.byKey[key] = cfg
	}
}

func (m *mcpConfigMerger) slice() []mcpServerConfig {
	out := make([]mcpServerConfig, 0, len(m.order))
	for _, key := range m.order {
		out = append(out, m.byKey[key])
	}
	return out
}

func readMCPConfigFile(path string, allowDirect bool) ([]mcpServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	configs, err := parseMCPConfigBytes(data, path, allowDirect)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return configs, nil
}

func parseMCPConfigBytes(data []byte, source string, allowDirect bool) ([]mcpServerConfig, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, nil
	}
	switch trimmed[0] {
	case '[':
		return parseMCPServerArray([]byte(trimmed), source)
	case '{':
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
			return nil, err
		}
		if raw, ok := obj["mcpServers"]; ok {
			return parseMCPServersValue(raw, source+".mcpServers")
		}
		if !allowDirect {
			return nil, nil
		}
		if looksLikeSingleMCPServer(obj) {
			var raw rawMCPServerConfig
			if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
				return nil, err
			}
			return normalizeMCPServerConfigs([]namedRawMCPServerConfig{{raw: raw}})
		}
		if looksLikeMCPServerMap(obj) {
			return parseMCPServerMap([]byte(trimmed), source)
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("expected JSON object or array")
	}
}

func parseMCPServersValue(raw json.RawMessage, source string) ([]mcpServerConfig, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		return parseMCPServerArray(raw, source)
	}
	if strings.HasPrefix(trimmed, "{") {
		return parseMCPServerMap(raw, source)
	}
	return nil, fmt.Errorf("%s must be an object or array", source)
}

func parseMCPServerArray(data []byte, source string) ([]mcpServerConfig, error) {
	var raws []rawMCPServerConfig
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	named := make([]namedRawMCPServerConfig, 0, len(raws))
	for _, raw := range raws {
		named = append(named, namedRawMCPServerConfig{raw: raw})
	}
	return normalizeMCPServerConfigs(named)
}

func parseMCPServerMap(data []byte, source string) ([]mcpServerConfig, error) {
	var raws map[string]rawMCPServerConfig
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	names := make([]string, 0, len(raws))
	for name := range raws {
		names = append(names, name)
	}
	sort.Strings(names)
	named := make([]namedRawMCPServerConfig, 0, len(names))
	for _, name := range names {
		named = append(named, namedRawMCPServerConfig{name: name, raw: raws[name]})
	}
	return normalizeMCPServerConfigs(named)
}

type namedRawMCPServerConfig struct {
	name string
	raw  rawMCPServerConfig
}

func normalizeMCPServerConfigs(raws []namedRawMCPServerConfig) ([]mcpServerConfig, error) {
	configs := make([]mcpServerConfig, 0, len(raws))
	for _, item := range raws {
		cfg, ok, err := normalizeMCPServerConfig(item.name, item.raw)
		if err != nil {
			return nil, err
		}
		if ok {
			configs = append(configs, cfg)
		}
	}
	return configs, nil
}

func normalizeMCPServerConfig(name string, raw rawMCPServerConfig) (mcpServerConfig, bool, error) {
	if raw.Disabled {
		return mcpServerConfig{}, false, nil
	}
	cfg := mcpServerConfig{
		Name:    firstNonEmptyString(raw.Name, name),
		Type:    normalizeMCPTransport(firstNonEmptyString(raw.Type, raw.Transport), raw),
		URL:     os.ExpandEnv(strings.TrimSpace(raw.URL)),
		Command: os.ExpandEnv(strings.TrimSpace(raw.Command)),
		Args:    expandStrings(raw.Args),
		Tools:   raw.Tools,
		Headers: expandStringMap(raw.Headers),
	}
	if len(cfg.Tools) == 0 {
		cfg.Tools = raw.ToolNameList
	}
	env, err := parseMCPEnv(raw.Env)
	if err != nil {
		return mcpServerConfig{}, false, fmt.Errorf("parse env for %s: %w", firstNonEmptyString(cfg.Name, name, raw.Command, raw.URL), err)
	}
	cfg.Env = env
	if cfg.Name == "" {
		cfg.Name = defaultMCPServerName(cfg)
	}
	return cfg, true, nil
}

func normalizeMCPTransport(typ string, raw rawMCPServerConfig) string {
	typ = strings.ToLower(strings.TrimSpace(typ))
	typ = strings.ReplaceAll(typ, "-", "_")
	if typ == "" && strings.TrimSpace(raw.Command) != "" {
		return "stdio"
	}
	if typ == "" && strings.TrimSpace(raw.URL) != "" {
		return "sse"
	}
	return typ
}

func parseMCPEnv(raw json.RawMessage) ([]string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var env []string
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, err
		}
		return expandStrings(env), nil
	}
	if strings.HasPrefix(trimmed, "{") {
		var envMap map[string]string
		if err := json.Unmarshal(raw, &envMap); err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(envMap))
		for key := range envMap {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		overrides := make([]string, 0, len(keys))
		for _, key := range keys {
			overrides = append(overrides, key+"="+os.ExpandEnv(envMap[key]))
		}
		return mergeEnv(os.Environ(), overrides), nil
	}
	return nil, fmt.Errorf("env must be an object or array")
}

func mergeEnv(base, overrides []string) []string {
	out := append([]string(nil), base...)
	index := map[string]int{}
	for i, entry := range out {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			index[key] = i
		}
	}
	for _, entry := range overrides {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			out = append(out, entry)
			continue
		}
		if existing, ok := index[key]; ok {
			out[existing] = entry
			continue
		}
		index[key] = len(out)
		out = append(out, entry)
	}
	return out
}

func expandStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = os.ExpandEnv(value)
	}
	return out
}

func expandStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = os.ExpandEnv(value)
	}
	return out
}

func looksLikeSingleMCPServer(obj map[string]json.RawMessage) bool {
	for key := range obj {
		if isMCPServerField(key) {
			return true
		}
	}
	return false
}

func looksLikeMCPServerMap(obj map[string]json.RawMessage) bool {
	if len(obj) == 0 {
		return false
	}
	for _, value := range obj {
		if !strings.HasPrefix(strings.TrimSpace(string(value)), "{") {
			return false
		}
	}
	return true
}

func isMCPServerField(key string) bool {
	switch key {
	case "name", "type", "transport", "url", "command", "args", "env", "tools", "toolNameList", "headers", "disabled":
		return true
	default:
		return false
	}
}

func defaultMCPServerName(cfg mcpServerConfig) string {
	switch {
	case cfg.Type == "stdio" && cfg.Command != "":
		return "stdio:" + cfg.Command
	case cfg.URL != "" && cfg.Type != "":
		return cfg.Type + ":" + cfg.URL
	case cfg.URL != "":
		return "mcp:" + cfg.URL
	case cfg.Command != "":
		return "stdio:" + cfg.Command
	default:
		return "mcp"
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func splitEnvList(raw string) []string {
	var out []string
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

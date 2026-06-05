package mcptools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	einomcp "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpsdk "github.com/mark3labs/mcp-go/mcp"
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
	if raw := strings.TrimSpace(os.Getenv("FINAL_EINO_MCP_SERVERS")); raw != "" {
		var configs []mcpServerConfig
		if err := json.Unmarshal([]byte(raw), &configs); err != nil {
			return nil, fmt.Errorf("parse FINAL_EINO_MCP_SERVERS: %w", err)
		}
		return configs, nil
	}

	var configs []mcpServerConfig
	for _, url := range splitEnvList(os.Getenv("FINAL_EINO_MCP_SSE_URLS")) {
		configs = append(configs, mcpServerConfig{
			Name: "sse:" + url,
			Type: "sse",
			URL:  url,
		})
	}
	for _, url := range splitEnvList(os.Getenv("FINAL_EINO_MCP_HTTP_URLS")) {
		configs = append(configs, mcpServerConfig{
			Name: "http:" + url,
			Type: "http",
			URL:  url,
		})
	}
	if raw := strings.TrimSpace(os.Getenv("FINAL_EINO_MCP_STDIO")); raw != "" {
		fields := strings.Fields(raw)
		if len(fields) > 0 {
			configs = append(configs, mcpServerConfig{
				Name:    "stdio:" + fields[0],
				Type:    "stdio",
				Command: fields[0],
				Args:    fields[1:],
				Env:     os.Environ(),
			})
		}
	}
	return configs, nil
}

func connectMCPServer(ctx context.Context, cfg mcpServerConfig) ([]tool.BaseTool, error) {
	typ := strings.ToLower(strings.TrimSpace(cfg.Type))
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

package mcptools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadMCPConfigsReadsProjectMCPJSON(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".mcp.json"), `{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "${HOME}/repo"],
      "env": {"TOKEN": "${MCP_TOKEN}", "PATH": "/custom/bin"},
      "toolNameList": ["read_file"]
    },
    "disabled": {
      "type": "sse",
      "url": "https://disabled.example/sse",
      "disabled": true
    }
  }
}`)
	t.Setenv("FINAL_EINO_MCP_SERVERS", "")
	t.Setenv("FINAL_EINO_MCP_SSE_URLS", "")
	t.Setenv("FINAL_EINO_MCP_HTTP_URLS", "")
	t.Setenv("FINAL_EINO_MCP_STDIO", "")
	t.Setenv("MCP_TOKEN", "secret")
	t.Setenv("HOME", "/home/test")
	t.Setenv("PATH", "/base/bin")

	configs, err := readMCPConfigsFrom(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 {
		t.Fatalf("configs = %#v, want one enabled config", configs)
	}
	cfg := configs[0]
	if cfg.Name != "filesystem" {
		t.Fatalf("name = %q, want filesystem", cfg.Name)
	}
	if cfg.Type != "stdio" {
		t.Fatalf("type = %q, want stdio", cfg.Type)
	}
	if cfg.Command != "npx" {
		t.Fatalf("command = %q, want npx", cfg.Command)
	}
	if got := strings.Join(cfg.Args, " "); got != "-y @modelcontextprotocol/server-filesystem /home/test/repo" {
		t.Fatalf("args = %q, want expanded args", got)
	}
	if got := strings.Join(cfg.Tools, ","); got != "read_file" {
		t.Fatalf("tools = %q, want read_file", got)
	}
	assertEnvEntry(t, cfg.Env, "TOKEN=secret")
	assertEnvEntry(t, cfg.Env, "PATH=/custom/bin")
}

func TestReadMCPConfigsReadsClaudeSettingsAndLocalOverrides(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".claude", "settings.json"), `{
  "mcpServers": {
    "linear": {"type": "sse", "url": "https://linear.example/sse"}
  }
}`)
	writeFile(t, filepath.Join(root, ".claude", "settings.local.json"), `{
  "mcpServers": {
    "linear": {"type": "http", "url": "https://linear.example/mcp", "headers": {"Authorization": "Bearer ${TOKEN}"}},
    "browser": {"type": "sse", "url": "https://browser.example/sse", "tools": ["tabs"]}
  }
}`)
	t.Setenv("FINAL_EINO_MCP_SERVERS", "")
	t.Setenv("FINAL_EINO_MCP_SSE_URLS", "")
	t.Setenv("FINAL_EINO_MCP_HTTP_URLS", "")
	t.Setenv("FINAL_EINO_MCP_STDIO", "")
	t.Setenv("TOKEN", "abc")

	configs, err := readMCPConfigsFrom(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 2 {
		t.Fatalf("configs = %#v, want two configs", configs)
	}
	linear := findConfig(t, configs, "linear")
	if linear.Type != "http" {
		t.Fatalf("linear type = %q, want local override http", linear.Type)
	}
	if linear.URL != "https://linear.example/mcp" {
		t.Fatalf("linear url = %q, want local override", linear.URL)
	}
	if linear.Headers["Authorization"] != "Bearer abc" {
		t.Fatalf("linear authorization header = %q, want expanded token", linear.Headers["Authorization"])
	}
	browser := findConfig(t, configs, "browser")
	if browser.Type != "sse" || browser.URL != "https://browser.example/sse" {
		t.Fatalf("browser config = %#v, want sse browser", browser)
	}
}

func TestReadMCPConfigsEnvServersTakesPrecedence(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".mcp.json"), `{
  "mcpServers": {
    "project": {"type": "sse", "url": "https://project.example/sse"}
  }
}`)
	t.Setenv("FINAL_EINO_MCP_SERVERS", `[
  {"name": "env", "transport": "streamable-http", "url": "https://env.example/mcp", "tools": ["search"]},
  {"name": "envstdio", "command": "node", "args": ["server.js"], "env": ["A=B"]}
]`)
	t.Setenv("FINAL_EINO_MCP_SSE_URLS", "https://ignored.example/sse")
	t.Setenv("FINAL_EINO_MCP_HTTP_URLS", "")
	t.Setenv("FINAL_EINO_MCP_STDIO", "")

	configs, err := readMCPConfigsFrom(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 2 {
		t.Fatalf("configs = %#v, want env configs only", configs)
	}
	httpCfg := findConfig(t, configs, "env")
	if httpCfg.Type != "streamable_http" || httpCfg.URL != "https://env.example/mcp" {
		t.Fatalf("env config = %#v, want streamable_http", httpCfg)
	}
	stdioCfg := findConfig(t, configs, "envstdio")
	if stdioCfg.Type != "stdio" || stdioCfg.Command != "node" {
		t.Fatalf("stdio env config = %#v, want inferred stdio", stdioCfg)
	}
	if len(stdioCfg.Env) != 1 || stdioCfg.Env[0] != "A=B" {
		t.Fatalf("stdio env = %#v, want explicit env array unchanged", stdioCfg.Env)
	}
}

func TestReadMCPConfigsAppendsEnvShortcuts(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FINAL_EINO_MCP_SERVERS", "")
	t.Setenv("FINAL_EINO_MCP_SSE_URLS", "https://a.example/sse, https://b.example/sse")
	t.Setenv("FINAL_EINO_MCP_HTTP_URLS", "https://c.example/mcp")
	t.Setenv("FINAL_EINO_MCP_STDIO", "node server.js --flag")

	configs, err := readMCPConfigsFrom(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 4 {
		t.Fatalf("configs = %#v, want 4 shortcut configs", configs)
	}
	if findConfig(t, configs, "sse:https://a.example/sse").Type != "sse" {
		t.Fatal("missing sse shortcut")
	}
	if findConfig(t, configs, "http:https://c.example/mcp").Type != "http" {
		t.Fatal("missing http shortcut")
	}
	stdio := findConfig(t, configs, "stdio:node")
	if strings.Join(stdio.Args, " ") != "server.js --flag" {
		t.Fatalf("stdio args = %#v, want server.js --flag", stdio.Args)
	}
	if len(stdio.Env) == 0 {
		t.Fatal("stdio shortcut should include current environment")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findConfig(t *testing.T, configs []mcpServerConfig, name string) mcpServerConfig {
	t.Helper()
	for _, cfg := range configs {
		if cfg.Name == name {
			return cfg
		}
	}
	t.Fatalf("config %q not found in %#v", name, configs)
	return mcpServerConfig{}
}

func assertEnvEntry(t *testing.T, env []string, want string) {
	t.Helper()
	for _, entry := range env {
		if entry == want {
			return
		}
	}
	t.Fatalf("env missing %q in %#v", want, env)
}

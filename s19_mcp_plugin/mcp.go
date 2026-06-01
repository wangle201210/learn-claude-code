package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/cloudwego/eino/schema"
)

// mcpToolDef 是 MCPClient 暴露的一个工具：名字、描述、参数 schema、handler。
// handler 接收原始 JSON args，返回结果字符串（与 builtin toolHandler 同型）。
type mcpToolDef struct {
	name        string
	description string
	params      *schema.ParamsOneOf
	handler     func(args string) string
}

// MCPClient 持有一个已连接的 MCP server 及其工具。教学版用 mock 实现，
// 真实场景下 tools/handler 来自远端 server 的 list_tools / call_tool RPC。
type MCPClient struct {
	name  string
	tools []mcpToolDef
}

var (
	mcpMu      sync.Mutex
	mcpClients = map[string]*MCPClient{}
)

var disallowedMCP = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// normalizeMCPName 把非 [A-Za-z0-9_-] 的字符替换为下划线，作为工具/服务器名前缀的一部分。
func normalizeMCPName(s string) string {
	return disallowedMCP.ReplaceAllString(s, "_")
}

// mockServers 是教学版预置的两个 MCP server 工厂：连接时调用工厂生成 MCPClient。
var mockServers = map[string]func() *MCPClient{
	"docs":   buildMockDocs,
	"deploy": buildMockDeploy,
}

func buildMockDocs() *MCPClient {
	return &MCPClient{
		name: "docs",
		tools: []mcpToolDef{
			{
				name:        "search",
				description: "Search documentation. (readOnly)",
				params: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
					"query": {Type: schema.String, Desc: "Search query.", Required: true},
				}),
				handler: func(args string) string {
					var a struct {
						Query string `json:"query"`
					}
					_ = json.Unmarshal([]byte(args), &a)
					return fmt.Sprintf("[docs] Found 3 results for '%s'", a.Query)
				},
			},
			{
				name:        "get_version",
				description: "Get API version. (readOnly)",
				params:      schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{}),
				handler: func(args string) string {
					return "[docs] API v2.1.0"
				},
			},
		},
	}
}

func buildMockDeploy() *MCPClient {
	return &MCPClient{
		name: "deploy",
		tools: []mcpToolDef{
			{
				name:        "trigger",
				description: "Trigger a deployment. (destructive — requires approval in real CC)",
				params: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
					"service": {Type: schema.String, Desc: "Service name.", Required: true},
				}),
				handler: func(args string) string {
					var a struct {
						Service string `json:"service"`
					}
					_ = json.Unmarshal([]byte(args), &a)
					return fmt.Sprintf("[deploy] Triggered: %s", a.Service)
				},
			},
			{
				name:        "status",
				description: "Check deployment status. (readOnly)",
				params: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
					"service": {Type: schema.String, Desc: "Service name.", Required: true},
				}),
				handler: func(args string) string {
					var a struct {
						Service string `json:"service"`
					}
					_ = json.Unmarshal([]byte(args), &a)
					return fmt.Sprintf("[deploy] %s: running (v1.4.2)", a.Service)
				},
			},
		},
	}
}

// connectMCP 连接到一个 mock MCP server：发现工具并注册到 mcpClients。
func connectMCP(name string) string {
	mcpMu.Lock()
	if _, ok := mcpClients[name]; ok {
		mcpMu.Unlock()
		return "MCP server '" + name + "' already connected"
	}
	factory, ok := mockServers[name]
	if !ok {
		var avail []string
		for k := range mockServers {
			avail = append(avail, k)
		}
		mcpMu.Unlock()
		return "Unknown server '" + name + "'. Available: " + strings.Join(avail, ", ")
	}
	client := factory()
	mcpClients[name] = client
	var names []string
	for _, t := range client.tools {
		names = append(names, t.name)
	}
	mcpMu.Unlock()
	fmt.Printf("  \033[31m[mcp] connected: %s → %v\033[0m\n", name, names)
	return fmt.Sprintf("Connected to MCP server '%s'. Discovered %d tools: %s",
		name, len(names), strings.Join(names, ", "))
}

// assembleToolInfos 把 builtin 工具 + 当前已连接 MCP 工具组装成动态池，
// MCP 工具用 mcp__{server}__{tool} 命名（servername/toolname 都过 normalize）。
func assembleToolInfos() []*schema.ToolInfo {
	infos := toolInfos()
	mcpMu.Lock()
	for serverName, client := range mcpClients {
		safeSrv := normalizeMCPName(serverName)
		for _, t := range client.tools {
			safeTool := normalizeMCPName(t.name)
			infos = append(infos, &schema.ToolInfo{
				Name:        "mcp__" + safeSrv + "__" + safeTool,
				Desc:        t.description,
				ParamsOneOf: t.params,
			})
		}
	}
	mcpMu.Unlock()
	return infos
}

// assembleHandlers 把 builtin handlers + MCP handlers 合到一张 dispatch 表里；
// MCP handler 用 closure 捕获对应的 mcpToolDef。
func assembleHandlers() map[string]toolHandler {
	m := handlers()
	mcpMu.Lock()
	for serverName, client := range mcpClients {
		safeSrv := normalizeMCPName(serverName)
		for _, t := range client.tools {
			t := t // capture-per-iteration
			safeTool := normalizeMCPName(t.name)
			key := "mcp__" + safeSrv + "__" + safeTool
			m[key] = func(ctx context.Context, args string) string {
				return t.handler(args)
			}
		}
	}
	mcpMu.Unlock()
	return m
}

// runConnectMCP 是 lead 端 connect_mcp 工具的 handler。
func runConnectMCP(ctx context.Context, args string) string {
	var a struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal([]byte(args), &a)
	if a.Name == "" {
		return "Error: 'name' required"
	}
	return connectMCP(a.Name)
}

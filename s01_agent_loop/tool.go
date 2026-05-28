package main

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
)

// bashTool 是本章唯一的工具：执行一条 shell 命令。
var bashTool = &schema.ToolInfo{
	Name: "bash",
	Desc: "Run a shell command.",
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"command": {
			Type:     schema.String,
			Desc:     "The shell command to run.",
			Required: true,
		},
	}),
}

// runBash 执行命令并返回合并后的 stdout+stderr。
// 拦截危险命令、限制 120s 超时、空输出与超长输出做兜底处理。
func runBash(ctx context.Context, command string) string {
	dangerous := []string{"rm -rf /", "sudo", "shutdown", "reboot", "> /dev/"}
	for _, d := range dangerous {
		if strings.Contains(command, d) {
			return "Error: Dangerous command blocked"
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	out, _ := exec.CommandContext(ctx, "bash", "-c", command).CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "Error: Timeout (120s)"
	}

	s := strings.TrimSpace(string(out))
	if s == "" {
		return "(no output)"
	}
	return truncate(s, 50000)
}

// truncate 按 rune 截断，避免切坏多字节字符（如中文）。
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// 基于文件的 MessageBus：每个 agent 一个 .mailboxes/{name}.jsonl 邮箱。
// 读取是销毁式的——读完即删除文件，避免重复消费。教学版没有锁文件，
// 真实 CC 用 proper-lockfile 处理并发写。
var mailboxDir = filepath.Join(workdir, ".mailboxes")

// busSend 把一条消息追加到目标邮箱。metadata 可为 nil；s16 起用于携带
// request_id 等协议字段。
func busSend(from, to, content, msgType string, metadata map[string]any) {
	_ = os.MkdirAll(mailboxDir, 0o755)
	if metadata == nil {
		metadata = map[string]any{}
	}
	msg := map[string]any{
		"from":     from,
		"to":       to,
		"content":  content,
		"type":     msgType,
		"ts":       time.Now().Unix(),
		"metadata": metadata,
	}
	data, _ := json.Marshal(msg)
	f, err := os.OpenFile(filepath.Join(mailboxDir, to+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
	fmt.Printf("  \033[33m[bus] %s → %s: (%s) %s\033[0m\n", from, to, msgType, truncate(content, 50))
}

// busReadInbox 一次性读出 agent 邮箱里的全部消息并删除文件。
func busReadInbox(agent string) []map[string]any {
	path := filepath.Join(mailboxDir, agent+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	_ = os.Remove(path)
	var msgs []map[string]any
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil {
			msgs = append(msgs, m)
		}
	}
	return msgs
}

// activeTeammates 跟踪已启动的 teammate（避免重名）。
var (
	teammateMu       sync.Mutex
	activeTeammates  = map[string]bool{}
	teammateAgent    model.ToolCallingChatModel // 绑定 teammate 工具集，main 里初始化
)

// teammateToolInfos：bash / read_file / write_file / send_message —— 没有
// spawn_teammate（防递归再开队伍）也没有 task 系列。
func teammateToolInfos() []*schema.ToolInfo {
	return []*schema.ToolInfo{
		{
			Name: "bash",
			Desc: "Run a shell command.",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"command": {Type: schema.String, Desc: "The shell command to run.", Required: true},
			}),
		},
		{
			Name: "read_file",
			Desc: "Read file contents.",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"path":  {Type: schema.String, Desc: "File path relative to the workspace.", Required: true},
				"limit": {Type: schema.Integer, Desc: "Max number of lines to read."},
			}),
		},
		{
			Name: "write_file",
			Desc: "Write content to a file.",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"path":    {Type: schema.String, Desc: "File path relative to the workspace.", Required: true},
				"content": {Type: schema.String, Desc: "The full content to write.", Required: true},
			}),
		},
		{
			Name: "send_message",
			Desc: "Send a message to another agent (e.g. 'lead').",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"to":      {Type: schema.String, Desc: "Recipient agent name.", Required: true},
				"content": {Type: schema.String, Desc: "Message body.", Required: true},
			}),
		},
		{
			// s16 新增：submit_plan 提交一个 plan 给 lead 审批（返回 request_id）。
			Name: "submit_plan",
			Desc: "Submit a plan to lead for approval. Returns a request_id; await plan_approval_response before acting.",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"plan": {Type: schema.String, Desc: "Plan body.", Required: true},
			}),
		},
	}
}

// spawnTeammateThread 在 goroutine 里跑一个 teammate agent：自己的 fresh
// messages、最多 10 轮工具循环、每轮先读 inbox 注入、结束时把最终文本
// 作为 result 发回 lead 邮箱。
func spawnTeammateThread(name, role, prompt string) string {
	teammateMu.Lock()
	if activeTeammates[name] {
		teammateMu.Unlock()
		return fmt.Sprintf("Teammate '%s' already exists", name)
	}
	activeTeammates[name] = true
	teammateMu.Unlock()

	sys := fmt.Sprintf("You are '%s', a %s. Use tools to complete the task. "+
		"Send your final result via send_message to 'lead'.", name, role)

	go func() {
		defer func() {
			_ = recover()
			teammateMu.Lock()
			delete(activeTeammates, name)
			teammateMu.Unlock()
			fmt.Printf("  \033[32m[teammate] %s finished\033[0m\n", name)
		}()

		ctx := context.Background()
		messages := []*schema.Message{
			schema.SystemMessage(sys),
			schema.UserMessage(prompt),
		}
		var lastText string
		shutdownReq := false

		// teammate 自己的 dispatch：send_message / submit_plan 用 closure 绑定 name。
		dispatch := map[string]toolHandler{
			"bash":       runBash,
			"read_file":  runRead,
			"write_file": runWrite,
			"send_message": func(ctx context.Context, args string) string {
				var a struct {
					To      string `json:"to"`
					Content string `json:"content"`
				}
				_ = json.Unmarshal([]byte(args), &a)
				if a.To == "" {
					return "Error: 'to' required"
				}
				busSend(name, a.To, a.Content, "message", nil)
				return "Sent"
			},
			"submit_plan": func(ctx context.Context, args string) string {
				var a struct {
					Plan string `json:"plan"`
				}
				_ = json.Unmarshal([]byte(args), &a)
				if a.Plan == "" {
					return "Error: 'plan' required"
				}
				return teammateSubmitPlan(name, a.Plan)
			},
		}

		// dispatchInbox 处理一批 inbox 消息：协议消息走 dispatch，普通消息注入 user。
		// 返回 (shutdown, gotNew)。
		dispatchInbox := func(inbox []map[string]any) (bool, bool) {
			var nonProto []map[string]any
			gotNew := false
			for _, msg := range inbox {
				t, _ := msg["type"].(string)
				meta, _ := msg["metadata"].(map[string]any)
				switch t {
				case "shutdown_request":
					reqID, _ := meta["request_id"].(string)
					busSend(name, "lead", "Shutting down gracefully.", "shutdown_response",
						map[string]any{"request_id": reqID, "approve": true})
					fmt.Printf("  \033[35m[protocol] %s approved shutdown (%s)\033[0m\n", name, reqID)
					return true, gotNew
				case "plan_approval_response":
					approve, _ := meta["approve"].(bool)
					content, _ := msg["content"].(string)
					if approve {
						messages = append(messages, schema.UserMessage("[Plan approved] Proceed with the task."))
					} else {
						messages = append(messages, schema.UserMessage("[Plan rejected] Feedback: "+content))
					}
					gotNew = true
				default:
					nonProto = append(nonProto, msg)
				}
			}
			if len(nonProto) > 0 {
				raw, _ := json.Marshal(nonProto)
				messages = append(messages, schema.UserMessage("<inbox>"+string(raw)+"</inbox>"))
				gotNew = true
			}
			return false, gotNew
		}

		// idle loop：最多 30 轮 LLM；无 tool_use 时 poll inbox 最多 maxIdleSec。
		const maxRounds = 30
		const maxIdleSec = 30
		for round := 0; round < maxRounds && !shutdownReq; round++ {
			stop, _ := dispatchInbox(busReadInbox(name))
			if stop {
				shutdownReq = true
				break
			}
			messages = enforcePairing(messages)

			resp, err := teammateAgent.Generate(ctx, messages)
			if err != nil {
				break
			}
			messages = append(messages, resp)
			if resp.Content != "" {
				lastText = resp.Content
			}

			if len(resp.ToolCalls) == 0 {
				// idle：等待 inbox 新消息或 shutdown，超时退出。
				deadline := time.Now().Add(maxIdleSec * time.Second)
				woken := false
				for time.Now().Before(deadline) && !shutdownReq {
					time.Sleep(time.Second)
					inbox := busReadInbox(name)
					if len(inbox) == 0 {
						continue
					}
					stop, gotNew := dispatchInbox(inbox)
					if stop {
						shutdownReq = true
						break
					}
					if gotNew {
						woken = true
						break
					}
				}
				if !woken {
					break // idle 超时，自然退出
				}
				continue
			}

			for _, tc := range resp.ToolCalls {
				var output string
				if h, ok := dispatch[tc.Function.Name]; ok {
					output = h(ctx, tc.Function.Arguments)
				} else {
					output = "Unknown: " + tc.Function.Name
				}
				messages = append(messages, schema.ToolMessage(output, tc.ID, schema.WithToolName(tc.Function.Name)))
			}
		}

		if strings.TrimSpace(lastText) == "" {
			lastText = "Done."
		}
		busSend(name, "lead", lastText, "result", nil)
	}()

	fmt.Printf("  \033[36m[teammate] %s spawned as %s\033[0m\n", name, role)
	return fmt.Sprintf("Teammate '%s' spawned as %s", name, role)
}

// ── Lead 端的工具 handlers ────────────────────────────────────

func runSpawnTeammate(ctx context.Context, args string) string {
	var a struct {
		Name   string `json:"name"`
		Role   string `json:"role"`
		Prompt string `json:"prompt"`
	}
	_ = json.Unmarshal([]byte(args), &a)
	if a.Name == "" || a.Prompt == "" {
		return "Error: name and prompt are required"
	}
	return spawnTeammateThread(a.Name, a.Role, a.Prompt)
}

func runSendMessage(ctx context.Context, args string) string {
	var a struct {
		To      string `json:"to"`
		Content string `json:"content"`
	}
	_ = json.Unmarshal([]byte(args), &a)
	if a.To == "" {
		return "Error: 'to' required"
	}
	busSend("lead", a.To, a.Content, "message", nil)
	return fmt.Sprintf("Sent to %s", a.To)
}

func runCheckInbox(ctx context.Context, args string) string {
	// s16: 走 consumeLeadInbox 让 *_response 协议消息自动 matchResponse。
	msgs := consumeLeadInbox(true)
	if len(msgs) == 0 {
		return "(inbox empty)"
	}
	var b strings.Builder
	for _, m := range msgs {
		from, _ := m["from"].(string)
		content, _ := m["content"].(string)
		msgType, _ := m["type"].(string)
		meta, _ := m["metadata"].(map[string]any)
		reqID, _ := meta["request_id"].(string)
		tag := " [" + msgType + "]"
		if reqID != "" {
			tag = fmt.Sprintf(" [%s req:%s]", msgType, reqID)
		}
		fmt.Fprintf(&b, "  [%s]%s %s\n", from, tag, truncate(content, 200))
	}
	return strings.TrimRight(b.String(), "\n")
}


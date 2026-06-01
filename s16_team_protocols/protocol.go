package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"
)

// ProtocolState 跟踪一个飞行中的协议请求。
type ProtocolState struct {
	RequestID string
	Type      string // "shutdown" | "plan_approval"
	Sender    string
	Target    string
	Status    string // "pending" | "approved" | "rejected"
	Payload   string
	CreatedAt int64
}

var (
	protocolMu       sync.Mutex
	pendingRequests  = map[string]*ProtocolState{}
)

func newRequestID() string {
	return fmt.Sprintf("req_%06d", rand.Intn(1000000))
}

// matchResponse 用 request_id 把响应关联回原请求，并做 type 校验。
func matchResponse(responseType, requestID string, approve bool) {
	protocolMu.Lock()
	defer protocolMu.Unlock()
	state, ok := pendingRequests[requestID]
	if !ok {
		fmt.Printf("  \033[31m[protocol] unknown request_id: %s\033[0m\n", requestID)
		return
	}
	if state.Type == "shutdown" && responseType != "shutdown_response" {
		fmt.Printf("  \033[31m[protocol] type mismatch: expected shutdown_response, got %s\033[0m\n", responseType)
		return
	}
	if state.Type == "plan_approval" && responseType != "plan_approval_response" {
		fmt.Printf("  \033[31m[protocol] type mismatch: expected plan_approval_response, got %s\033[0m\n", responseType)
		return
	}
	if state.Status != "pending" {
		fmt.Printf("  \033[33m[protocol] %s already %s, ignoring duplicate\033[0m\n", requestID, state.Status)
		return
	}
	if approve {
		state.Status = "approved"
	} else {
		state.Status = "rejected"
	}
	icon, color := "✓", "32"
	if !approve {
		icon, color = "✗", "31"
	}
	fmt.Printf("  \033[%sm[protocol] %s %s (%s: %s)\033[0m\n", color, state.Type, icon, requestID, state.Status)
}

// consumeLeadInbox 读 lead 邮箱；routeProtocol=true 时自动把 *_response 类型
// 的消息路由给 matchResponse，更新 pendingRequests 的状态机。返回全部消息。
func consumeLeadInbox(routeProtocol bool) []map[string]any {
	msgs := busReadInbox("lead")
	if !routeProtocol {
		return msgs
	}
	for _, msg := range msgs {
		msgType, _ := msg["type"].(string)
		meta, _ := msg["metadata"].(map[string]any)
		reqID, _ := meta["request_id"].(string)
		if reqID == "" || !strings.HasSuffix(msgType, "_response") {
			continue
		}
		approve, _ := meta["approve"].(bool)
		matchResponse(msgType, reqID, approve)
	}
	return msgs
}

// ── Lead 协议工具 handlers ────────────────────────────────────

func runRequestShutdown(ctx context.Context, args string) string {
	var a struct {
		Teammate string `json:"teammate"`
	}
	_ = json.Unmarshal([]byte(args), &a)
	if a.Teammate == "" {
		return "Error: 'teammate' required"
	}
	reqID := newRequestID()
	protocolMu.Lock()
	pendingRequests[reqID] = &ProtocolState{
		RequestID: reqID, Type: "shutdown",
		Sender: "lead", Target: a.Teammate,
		Status: "pending", CreatedAt: time.Now().Unix(),
	}
	protocolMu.Unlock()
	busSend("lead", a.Teammate, "Please shut down gracefully.", "shutdown_request",
		map[string]any{"request_id": reqID})
	fmt.Printf("  \033[35m[protocol] shutdown_request → %s (%s)\033[0m\n", a.Teammate, reqID)
	return fmt.Sprintf("Shutdown request sent to %s (req: %s)", a.Teammate, reqID)
}

func runRequestPlan(ctx context.Context, args string) string {
	var a struct {
		Teammate string `json:"teammate"`
		Task     string `json:"task"`
	}
	_ = json.Unmarshal([]byte(args), &a)
	if a.Teammate == "" || a.Task == "" {
		return "Error: 'teammate' and 'task' required"
	}
	busSend("lead", a.Teammate, "Please submit a plan for: "+a.Task, "message", nil)
	return fmt.Sprintf("Asked %s to submit a plan", a.Teammate)
}

func runReviewPlan(ctx context.Context, args string) string {
	var a struct {
		RequestID string `json:"request_id"`
		Approve   bool   `json:"approve"`
		Feedback  string `json:"feedback"`
	}
	_ = json.Unmarshal([]byte(args), &a)
	protocolMu.Lock()
	state, ok := pendingRequests[a.RequestID]
	if !ok {
		protocolMu.Unlock()
		return fmt.Sprintf("Request %s not found", a.RequestID)
	}
	if state.Status != "pending" {
		s := state.Status
		protocolMu.Unlock()
		return fmt.Sprintf("Request %s already %s", a.RequestID, s)
	}
	if a.Approve {
		state.Status = "approved"
	} else {
		state.Status = "rejected"
	}
	target := state.Sender // 请求是 teammate 提交的，回给 teammate
	protocolMu.Unlock()

	body := a.Feedback
	if body == "" {
		if a.Approve {
			body = "Approved"
		} else {
			body = "Rejected"
		}
	}
	busSend("lead", target, body, "plan_approval_response",
		map[string]any{"request_id": a.RequestID, "approve": a.Approve})
	icon := "✓"
	if !a.Approve {
		icon = "✗"
	}
	fmt.Printf("  \033[32m[protocol] plan %s (%s)\033[0m\n", icon, a.RequestID)
	verb := "approved"
	if !a.Approve {
		verb = "rejected"
	}
	return fmt.Sprintf("Plan %s (%s)", verb, a.RequestID)
}

// teammateSubmitPlan 是 teammate 端 submit_plan 工具的实际逻辑：注册
// pending_requests 后发 plan_approval_request 给 lead。
func teammateSubmitPlan(from, plan string) string {
	reqID := newRequestID()
	protocolMu.Lock()
	pendingRequests[reqID] = &ProtocolState{
		RequestID: reqID, Type: "plan_approval",
		Sender: from, Target: "lead",
		Status: "pending", Payload: plan, CreatedAt: time.Now().Unix(),
	}
	protocolMu.Unlock()
	busSend(from, "lead", plan, "plan_approval_request",
		map[string]any{"request_id": reqID})
	return fmt.Sprintf("Plan submitted (%s). Waiting for approval...", reqID)
}

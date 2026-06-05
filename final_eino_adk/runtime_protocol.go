package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type sendMessageArgs struct {
	To      string `json:"to" jsonschema:"required" jsonschema_description:"Mailbox recipient name"`
	From    string `json:"from,omitempty" jsonschema_description:"Sender name, defaults to FinalAgent"`
	Kind    string `json:"kind,omitempty" jsonschema_description:"Message kind, such as note, plan_request, shutdown_request"`
	Subject string `json:"subject,omitempty" jsonschema_description:"Short message subject"`
	Body    string `json:"body" jsonschema:"required" jsonschema_description:"Message body"`
}

type checkInboxArgs struct {
	Name  string `json:"name,omitempty" jsonschema_description:"Mailbox name, defaults to main"`
	Limit int    `json:"limit,omitempty" jsonschema_description:"Maximum messages to return, default 20"`
}

type requestShutdownArgs struct {
	Reason string `json:"reason" jsonschema:"required" jsonschema_description:"Why the agent should stop or hand control back"`
}

type requestPlanArgs struct {
	Request string `json:"request" jsonschema:"required" jsonschema_description:"The plan request or decision that needs approval"`
}

type reviewPlanArgs struct {
	RequestID string `json:"request_id" jsonschema:"required" jsonschema_description:"Plan request id"`
	Decision  string `json:"decision" jsonschema:"required,enum=approved,enum=changes_requested,enum=rejected" jsonschema_description:"Review decision"`
	Comments  string `json:"comments,omitempty" jsonschema_description:"Review comments"`
}

func (r *agentRuntime) sendMessage(input *sendMessageArgs) (string, error) {
	to, err := validateMailboxName(input.To)
	if err != nil {
		return "", err
	}
	from := strings.TrimSpace(input.From)
	if from == "" {
		from = "FinalAgent"
	}
	kind := strings.TrimSpace(input.Kind)
	if kind == "" {
		kind = "note"
	}
	msg := mailboxMessage{
		ID:      fmt.Sprintf("msg-%d", time.Now().UnixNano()),
		Time:    time.Now().Format(time.RFC3339),
		From:    from,
		To:      to,
		Kind:    kind,
		Subject: input.Subject,
		Body:    input.Body,
	}
	if strings.TrimSpace(msg.Body) == "" {
		return "", errors.New("body is required")
	}
	if err := r.appendMessage(msg); err != nil {
		return "", err
	}
	return fmt.Sprintf("Sent %s to %s.", msg.ID, to), nil
}

func (r *agentRuntime) checkInbox(input *checkInboxArgs) (string, error) {
	name := input.Name
	if name == "" {
		name = "main"
	}
	name, err := validateMailboxName(name)
	if err != nil {
		return "", err
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 20
	}
	msgs, err := r.readMailbox(name)
	if err != nil {
		return "", err
	}
	if len(msgs) == 0 {
		return "No messages.", nil
	}
	if len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	return formatMessages(msgs), nil
}

func (r *agentRuntime) requestShutdown(input *requestShutdownArgs) (string, error) {
	return r.sendMessage(&sendMessageArgs{
		To:      "main",
		From:    "protocol",
		Kind:    "shutdown_request",
		Subject: "shutdown requested",
		Body:    input.Reason,
	})
}

func (r *agentRuntime) requestPlan(input *requestPlanArgs) (string, error) {
	id := fmt.Sprintf("plan-%d", time.Now().UnixNano())
	if _, err := r.sendMessage(&sendMessageArgs{
		To:      "main",
		From:    "protocol",
		Kind:    "plan_request",
		Subject: id,
		Body:    input.Request,
	}); err != nil {
		return "", err
	}
	return "Created plan request " + id + ".", nil
}

func (r *agentRuntime) reviewPlan(input *reviewPlanArgs) (string, error) {
	decision := strings.TrimSpace(input.Decision)
	if decision != "approved" && decision != "changes_requested" && decision != "rejected" {
		return "", errors.New("decision must be approved, changes_requested, or rejected")
	}
	body := strings.TrimSpace(input.Comments)
	if body == "" {
		body = "decision: " + decision
	}
	return r.sendMessage(&sendMessageArgs{
		To:      "main",
		From:    "protocol",
		Kind:    "plan_review",
		Subject: input.RequestID + ":" + decision,
		Body:    body,
	})
}

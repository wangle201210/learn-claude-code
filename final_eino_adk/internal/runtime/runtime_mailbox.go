package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type mailboxMessage struct {
	ID       string            `json:"id"`
	Time     string            `json:"time"`
	From     string            `json:"from"`
	To       string            `json:"to"`
	Kind     string            `json:"kind"`
	Subject  string            `json:"subject,omitempty"`
	Body     string            `json:"body"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func (r *Runtime) appendMessage(msg mailboxMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := os.MkdirAll(r.mailboxDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(r.mailboxDir, msg.To+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(msg)
}

func (r *Runtime) readMailbox(name string) ([]mailboxMessage, error) {
	data, err := os.ReadFile(filepath.Join(r.mailboxDir, name+".jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var msgs []mailboxMessage
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var msg mailboxMessage
		if err := json.Unmarshal([]byte(line), &msg); err == nil {
			msgs = append(msgs, msg)
		}
	}
	return msgs, nil
}

func (r *Runtime) collectMainInbox() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	path := filepath.Join(r.mailboxDir, "main.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if int64(len(data)) < r.mainInboxOffset {
		r.mainInboxOffset = int64(len(data))
		return nil
	}
	chunk := string(data[r.mainInboxOffset:])
	r.mainInboxOffset = int64(len(data))

	var notes []string
	for _, line := range strings.Split(strings.TrimSpace(chunk), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var msg mailboxMessage
		if err := json.Unmarshal([]byte(line), &msg); err == nil {
			notes = append(notes, fmt.Sprintf("<message_notification id=%q kind=%q from=%q>\n%s\n</message_notification>", msg.ID, msg.Kind, msg.From, formatMessages([]mailboxMessage{msg})))
		}
	}
	return notes
}

func (r *Runtime) currentMailboxSize(name string) int64 {
	path := filepath.Join(r.mailboxDir, name+".jsonl")
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func formatMessages(msgs []mailboxMessage) string {
	var b strings.Builder
	for _, msg := range msgs {
		fmt.Fprintf(&b, "%s [%s] %s -> %s", msg.ID, msg.Kind, msg.From, msg.To)
		if msg.Subject != "" {
			fmt.Fprintf(&b, " subject=%q", msg.Subject)
		}
		fmt.Fprintf(&b, "\n%s\n\n", msg.Body)
	}
	return strings.TrimSpace(b.String())
}

var mailboxNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func validateMailboxName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("name is required")
	}
	if !mailboxNameRE.MatchString(name) || strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid name %q", name)
	}
	return name, nil
}

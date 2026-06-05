package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

type cronJob struct {
	ID        string `json:"id"`
	Cron      string `json:"cron"`
	Prompt    string `json:"prompt"`
	Recurring bool   `json:"recurring"`
	Durable   bool   `json:"durable"`
	LastRun   string `json:"last_run,omitempty"`
	CreatedAt string `json:"created_at"`
}

type scheduleCronArgs struct {
	ID        string `json:"id,omitempty" jsonschema_description:"Optional stable job id"`
	Cron      string `json:"cron" jsonschema:"required" jsonschema_description:"Five-field cron expression: minute hour day-of-month month day-of-week"`
	Prompt    string `json:"prompt" jsonschema:"required" jsonschema_description:"Prompt to inject when the cron fires"`
	Recurring bool   `json:"recurring,omitempty" jsonschema_description:"Whether the job should keep running after it fires"`
	Durable   bool   `json:"durable,omitempty" jsonschema_description:"Persist the job in .scheduled_tasks.json"`
}

type cronIDArgs struct {
	ID string `json:"id" jsonschema:"required" jsonschema_description:"Cron job id"`
}

func (r *Runtime) scheduleCron(input *scheduleCronArgs) (string, error) {
	if err := validateCron(input.Cron); err != nil {
		return "", err
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return "", errors.New("prompt is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	id := strings.TrimSpace(input.ID)
	if id == "" {
		id = fmt.Sprintf("cron-%d", time.Now().UnixNano())
	}
	if _, err := validateMailboxName(id); err != nil {
		return "", fmt.Errorf("invalid cron id: %w", err)
	}
	r.crons[id] = &cronJob{
		ID:        id,
		Cron:      strings.TrimSpace(input.Cron),
		Prompt:    input.Prompt,
		Recurring: input.Recurring,
		Durable:   input.Durable,
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	r.saveCronsLocked()
	return fmt.Sprintf("Scheduled cron %s.", id), nil
}

func (r *Runtime) listCrons() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.crons) == 0 {
		return "No scheduled cron jobs."
	}
	ids := make([]string, 0, len(r.crons))
	for id := range r.crons {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var b strings.Builder
	for _, id := range ids {
		job := r.crons[id]
		fmt.Fprintf(&b, "%s cron=%q recurring=%v durable=%v last_run=%q\nprompt: %s\n\n",
			job.ID, job.Cron, job.Recurring, job.Durable, job.LastRun, job.Prompt)
	}
	return strings.TrimSpace(b.String())
}

func (r *Runtime) cancelCron(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.crons[id]; !ok {
		return "No such cron: " + id, nil
	}
	delete(r.crons, id)
	r.saveCronsLocked()
	return "Cancelled cron " + id + ".", nil
}

func (r *Runtime) cronLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			r.tickCrons(now)
		}
	}
}

func (r *Runtime) tickCrons(now time.Time) {
	if now.Second() != 0 {
		return
	}
	runKey := now.Format("2006-01-02T15:04")

	r.mu.Lock()
	defer r.mu.Unlock()
	changed := false
	for id, job := range r.crons {
		if job.LastRun == runKey || !cronMatches(job.Cron, now) {
			continue
		}
		job.LastRun = runKey
		changed = true
		r.cronQueue = append(r.cronQueue, fmt.Sprintf("<cron_notification id=%q cron=%q>\n%s\n</cron_notification>", id, job.Cron, job.Prompt))
		if !job.Recurring {
			delete(r.crons, id)
		}
	}
	if changed {
		r.saveCronsLocked()
	}
}

func (r *Runtime) loadCrons() {
	data, err := os.ReadFile(r.cronFile)
	if err != nil {
		return
	}
	var jobs []*cronJob
	if err := json.Unmarshal(data, &jobs); err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, job := range jobs {
		if job != nil && job.ID != "" {
			r.crons[job.ID] = job
		}
	}
}

func (r *Runtime) saveCronsLocked() {
	var jobs []*cronJob
	for _, job := range r.crons {
		if job.Durable {
			jobs = append(jobs, job)
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(r.cronFile, data, 0o644)
}

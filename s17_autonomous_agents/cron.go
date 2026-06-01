package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CronJob 是注册的一条定时任务。
type CronJob struct {
	ID        string `json:"id"`
	Cron      string `json:"cron"`      // 5 字段 cron 表达式
	Prompt    string `json:"prompt"`    // 触发时注入给 agent 的消息
	Recurring bool   `json:"recurring"` // false = 触发一次后自删
	Durable   bool   `json:"durable"`   // true = 落盘 .scheduled_tasks.json
}

var (
	cronMu         sync.Mutex
	scheduledJobs  = map[string]*CronJob{}
	cronQueue      []*CronJob
	lastFired      = map[string]string{} // job_id → "YYYY-MM-DD HH:MM"
	durablePath    = filepath.Join(workdir, ".scheduled_tasks.json")
	cronStartOnce  sync.Once
)

// ── 字段匹配 ──

func cronFieldMatches(field string, value int) bool {
	switch {
	case field == "*":
		return true
	case strings.HasPrefix(field, "*/"):
		step, err := strconv.Atoi(field[2:])
		return err == nil && step > 0 && value%step == 0
	case strings.Contains(field, ","):
		for _, part := range strings.Split(field, ",") {
			if cronFieldMatches(strings.TrimSpace(part), value) {
				return true
			}
		}
		return false
	case strings.Contains(field, "-"):
		parts := strings.SplitN(field, "-", 2)
		lo, err1 := strconv.Atoi(parts[0])
		hi, err2 := strconv.Atoi(parts[1])
		return err1 == nil && err2 == nil && value >= lo && value <= hi
	default:
		n, err := strconv.Atoi(field)
		return err == nil && value == n
	}
}

// cronMatches 标准 5 字段语义：DOM 与 DOW 都被约束时取 OR。
func cronMatches(expr string, dt time.Time) bool {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return false
	}
	minute, hour, dom, month, dow := fields[0], fields[1], fields[2], fields[3], fields[4]
	dowVal := int(dt.Weekday()) // Go: Sunday=0，与 cron 一致

	if !cronFieldMatches(minute, dt.Minute()) ||
		!cronFieldMatches(hour, dt.Hour()) ||
		!cronFieldMatches(month, int(dt.Month())) {
		return false
	}
	domOK := cronFieldMatches(dom, dt.Day())
	dowOK := cronFieldMatches(dow, dowVal)
	switch {
	case dom == "*" && dow == "*":
		return true
	case dom == "*":
		return dowOK
	case dow == "*":
		return domOK
	default:
		return domOK || dowOK // OR 语义
	}
}

// ── 校验 ──

func validateCronField(field string, lo, hi int) string {
	switch {
	case field == "*":
		return ""
	case strings.HasPrefix(field, "*/"):
		step, err := strconv.Atoi(field[2:])
		if err != nil {
			return "invalid step: " + field
		}
		if step <= 0 {
			return "step must be > 0: " + field
		}
		return ""
	case strings.Contains(field, ","):
		for _, part := range strings.Split(field, ",") {
			if e := validateCronField(strings.TrimSpace(part), lo, hi); e != "" {
				return e
			}
		}
		return ""
	case strings.Contains(field, "-"):
		parts := strings.SplitN(field, "-", 2)
		a, err1 := strconv.Atoi(parts[0])
		b, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			return "invalid range: " + field
		}
		if a < lo || a > hi || b < lo || b > hi {
			return fmt.Sprintf("range %s out of bounds [%d-%d]", field, lo, hi)
		}
		if a > b {
			return "range start > end: " + field
		}
		return ""
	default:
		v, err := strconv.Atoi(field)
		if err != nil {
			return "invalid field: " + field
		}
		if v < lo || v > hi {
			return fmt.Sprintf("value %d out of bounds [%d-%d]", v, lo, hi)
		}
		return ""
	}
}

func validateCron(expr string) string {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return fmt.Sprintf("expected 5 fields, got %d", len(fields))
	}
	bounds := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	names := [5]string{"minute", "hour", "day-of-month", "month", "day-of-week"}
	for i, f := range fields {
		if e := validateCronField(f, bounds[i][0], bounds[i][1]); e != "" {
			return names[i] + ": " + e
		}
	}
	return ""
}

// ── 注册 / 取消 ──

func scheduleJob(expr, prompt string, recurring, durable bool) (*CronJob, string) {
	if e := validateCron(expr); e != "" {
		return nil, e
	}
	job := &CronJob{
		ID:        fmt.Sprintf("cron_%06d", rand.Intn(1000000)),
		Cron:      expr,
		Prompt:    prompt,
		Recurring: recurring,
		Durable:   durable,
	}
	cronMu.Lock()
	scheduledJobs[job.ID] = job
	cronMu.Unlock()
	if durable {
		saveDurableJobs()
	}
	fmt.Printf("  \033[35m[cron register] %s '%s' → %s\033[0m\n", job.ID, expr, truncate(prompt, 40))
	return job, ""
}

func cancelJob(id string) string {
	cronMu.Lock()
	job, ok := scheduledJobs[id]
	if ok {
		delete(scheduledJobs, id)
	}
	cronMu.Unlock()
	if !ok {
		return "Job " + id + " not found"
	}
	if job.Durable {
		saveDurableJobs()
	}
	fmt.Printf("  \033[31m[cron cancel] %s\033[0m\n", id)
	return "Cancelled " + id
}

// ── 持久化 ──

func saveDurableJobs() {
	cronMu.Lock()
	var durable []*CronJob
	for _, j := range scheduledJobs {
		if j.Durable {
			durable = append(durable, j)
		}
	}
	cronMu.Unlock()
	data, _ := json.MarshalIndent(durable, "", "  ")
	_ = os.WriteFile(durablePath, data, 0o644)
}

func loadDurableJobs() {
	data, err := os.ReadFile(durablePath)
	if err != nil {
		return
	}
	var jobs []*CronJob
	if json.Unmarshal(data, &jobs) != nil {
		return
	}
	var valid int
	cronMu.Lock()
	for _, j := range jobs {
		if e := validateCron(j.Cron); e != "" {
			fmt.Printf("  \033[31m[cron] skipping invalid job %s: %s\033[0m\n", j.ID, e)
			continue
		}
		scheduledJobs[j.ID] = j
		valid++
	}
	cronMu.Unlock()
	if valid > 0 {
		fmt.Printf("  \033[35m[cron] loaded %d durable job(s)\033[0m\n", valid)
	}
}

// ── 队列消费 ──

func consumeCronQueue() []*CronJob {
	cronMu.Lock()
	fired := cronQueue
	cronQueue = nil
	cronMu.Unlock()
	return fired
}

func hasCronQueue() bool {
	cronMu.Lock()
	defer cronMu.Unlock()
	return len(cronQueue) > 0
}

// ── Scheduler daemon ──

// cronSchedulerLoop 每秒检查时间，命中的 job 推入 cronQueue。
// 单条 job 的异常用 defer/recover 拦下，避免拖垮整条调度线。
func cronSchedulerLoop() {
	for {
		time.Sleep(time.Second)
		now := time.Now()
		marker := now.Format("2006-01-02 15:04")
		var needSave bool

		cronMu.Lock()
		for id, job := range scheduledJobs {
			func() {
				defer func() { _ = recover() }()
				if !cronMatches(job.Cron, now) {
					return
				}
				if lastFired[id] == marker {
					return
				}
				cronQueue = append(cronQueue, job)
				lastFired[id] = marker
				fmt.Printf("  \033[35m[cron fire] %s → %s\033[0m\n", id, truncate(job.Prompt, 40))
				if !job.Recurring {
					delete(scheduledJobs, id)
					if job.Durable {
						needSave = true
					}
				}
			}()
		}
		cronMu.Unlock()

		// 解锁后再保存，避免 saveDurableJobs 再次取锁导致死锁。
		if needSave {
			saveDurableJobs()
		}
	}
}

// startCronOnce 启动 scheduler goroutine（只启一次）；同时加载 durable 作业。
func startCronOnce() {
	cronStartOnce.Do(func() {
		loadDurableJobs()
		go cronSchedulerLoop()
		fmt.Println("  \033[35m[cron] scheduler thread started\033[0m")
	})
}

// ── 工具 handlers ──

func runScheduleCron(ctx context.Context, args string) string {
	var a struct {
		Cron      string `json:"cron"`
		Prompt    string `json:"prompt"`
		Recurring *bool  `json:"recurring"`
		Durable   *bool  `json:"durable"`
	}
	_ = json.Unmarshal([]byte(args), &a)
	if a.Cron == "" || a.Prompt == "" {
		return "Error: cron and prompt are required"
	}
	recurring := true
	if a.Recurring != nil {
		recurring = *a.Recurring
	}
	durable := true
	if a.Durable != nil {
		durable = *a.Durable
	}
	job, err := scheduleJob(a.Cron, a.Prompt, recurring, durable)
	if err != "" {
		return "Error: " + err
	}
	return fmt.Sprintf("Scheduled %s: '%s' → %s", job.ID, job.Cron, a.Prompt)
}

func runListCrons(ctx context.Context, args string) string {
	cronMu.Lock()
	jobs := make([]*CronJob, 0, len(scheduledJobs))
	for _, j := range scheduledJobs {
		jobs = append(jobs, j)
	}
	cronMu.Unlock()
	if len(jobs) == 0 {
		return "No cron jobs. Use schedule_cron to add one."
	}
	var b strings.Builder
	for _, j := range jobs {
		tag := "one-shot"
		if j.Recurring {
			tag = "recurring"
		}
		dur := "session"
		if j.Durable {
			dur = "durable"
		}
		fmt.Fprintf(&b, "  %s: '%s' → %s [%s, %s]\n", j.ID, j.Cron, truncate(j.Prompt, 40), tag, dur)
	}
	return strings.TrimRight(b.String(), "\n")
}

func runCancelCron(ctx context.Context, args string) string {
	var a struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal([]byte(args), &a)
	return cancelJob(a.JobID)
}

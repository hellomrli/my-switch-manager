package server

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ============================ 后台任务 ============================
//
// 扫描、整理、合并、下载都是长任务：同一时刻只允许一个会改动文件或状态的任务，
// 前端轮询 /api/jobs/current 拿进度与增量日志。

type Job struct {
	ID       int       `json:"id"`
	Kind     string    `json:"kind"`
	Title    string    `json:"title"`
	State    string    `json:"state"`    // running / done / failed / cancelled
	Progress float64   `json:"progress"` // 0..1；<0 表示无法估计
	Stage    string    `json:"stage"`
	Error    string    `json:"error,omitempty"`
	Summary  string    `json:"summary,omitempty"`
	Started  time.Time `json:"started"`
	Ended    time.Time `json:"ended,omitempty"`
	LogCount int       `json:"logCount"`

	logs   []string
	cancel context.CancelFunc
}

type JobCtx struct {
	context.Context
	r   *Jobs
	job *Job
}

func (c *JobCtx) Logf(format string, args ...any) {
	line := time.Now().Format("15:04:05 ") + fmt.Sprintf(format, args...)
	c.r.mu.Lock()
	c.job.logs = append(c.job.logs, line)
	if len(c.job.logs) > 5000 {
		c.job.logs = c.job.logs[len(c.job.logs)-4000:]
	}
	c.job.LogCount = len(c.job.logs)
	c.r.mu.Unlock()
}

func (c *JobCtx) Progress(p float64, stage string) {
	c.r.mu.Lock()
	c.job.Progress, c.job.Stage = p, stage
	c.r.mu.Unlock()
}

func (c *JobCtx) Summary(s string) {
	c.r.mu.Lock()
	c.job.Summary = s
	c.r.mu.Unlock()
}

// DefaultSummary 只在任务还没有摘要时填写：整理/合并后顺带的重新扫描不应盖掉真正的结果
func (c *JobCtx) DefaultSummary(s string) {
	c.r.mu.Lock()
	if c.job.Summary == "" {
		c.job.Summary = s
	}
	c.r.mu.Unlock()
}

type Jobs struct {
	mu      sync.Mutex
	nextID  int
	current *Job
	history []*Job
}

var ErrBusy = fmt.Errorf("已有任务在运行，请等待完成或取消")

// Start 启动任务；已有任务在跑时返回 ErrBusy
func (r *Jobs) Start(kind, title string, fn func(*JobCtx) error) (*Job, error) {
	r.mu.Lock()
	if r.current != nil && r.current.State == "running" {
		r.mu.Unlock()
		return nil, ErrBusy
	}
	r.nextID++
	ctx, cancel := context.WithCancel(context.Background())
	j := &Job{ID: r.nextID, Kind: kind, Title: title, State: "running", Progress: -1, Started: time.Now(), cancel: cancel}
	r.current = j
	r.history = append(r.history, j)
	if len(r.history) > 30 {
		r.history = r.history[len(r.history)-30:]
	}
	// Copy this job while locked, before its worker can mutate it.
	cp := *j
	cp.cancel = nil
	r.mu.Unlock()

	jc := &JobCtx{Context: ctx, r: r, job: j}
	go func() {
		var err error
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("内部错误: %v", p)
			}
			r.mu.Lock()
			j.Ended = time.Now()
			switch {
			case ctx.Err() != nil:
				j.State, j.Error = "cancelled", "已取消"
			case err != nil:
				j.State, j.Error = "failed", err.Error()
			default:
				j.State, j.Progress = "done", 1
			}
			r.mu.Unlock()
			cancel()
			if err != nil {
				jc.Logf("[错误] %v", err)
			}
		}()
		err = fn(jc)
	}()
	return &cp, nil
}

func (r *Jobs) Cancel() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current == nil || r.current.State != "running" {
		return false
	}
	r.current.cancel()
	return true
}

// Snapshot 返回当前（或最近一个）任务及 since 之后的日志
func (r *Jobs) Snapshot(since int) (*Job, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current == nil {
		return nil, nil
	}
	cp := *r.current
	var logs []string
	if since < len(r.current.logs) && since >= 0 {
		logs = append(logs, r.current.logs[since:]...)
	}
	return &cp, logs
}

func (r *Jobs) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current != nil && r.current.State == "running"
}

func (r *Jobs) History() []Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Job, 0, len(r.history))
	for i := len(r.history) - 1; i >= 0; i-- {
		out = append(out, *r.history[i])
	}
	return out
}

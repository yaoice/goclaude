// Package task 定义任务系统领域模型
package task

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// TaskType 任务类型
type TaskType string

const (
	TaskTypeLocalBash   TaskType = "local_bash"
	TaskTypeLocalAgent  TaskType = "local_agent"
	TaskTypeRemoteAgent TaskType = "remote_agent"
	TaskTypeWorkflow    TaskType = "local_workflow"
	TaskTypeMonitor     TaskType = "monitor_mcp"
)

// TaskStatus 任务状态
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusKilled    TaskStatus = "killed"
)

// Task 任务实体
type Task struct {
	mu sync.RWMutex

	// ID 任务唯一标识
	ID string `json:"id"`
	// Type 任务类型
	Type TaskType `json:"type"`
	// Status 任务状态
	Status TaskStatus `json:"status"`
	// Name 任务名称/描述
	Name string `json:"name"`
	// Command 执行的命令（对于bash类型）
	Command string `json:"command,omitempty"`
	// Prompt 任务提示词（对于agent类型）
	Prompt string `json:"prompt,omitempty"`
	// WorkingDir 工作目录
	WorkingDir string `json:"working_dir"`
	// Output 任务输出
	Output string `json:"output"`
	// Error 错误信息
	Error string `json:"error,omitempty"`

	// CreatedAt 创建时间
	CreatedAt time.Time `json:"created_at"`
	// StartedAt 开始执行时间
	StartedAt *time.Time `json:"started_at,omitempty"`
	// CompletedAt 完成时间
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	// cancel 取消函数
	cancel context.CancelFunc `json:"-"`
}

// NewTask 创建新任务
func NewTask(id string, taskType TaskType, name string) *Task {
	return &Task{
		ID:        id,
		Type:      taskType,
		Status:    TaskStatusPending,
		Name:      name,
		CreatedAt: time.Now(),
	}
}

// Start 将任务标记为运行中
func (t *Task) Start(cancel context.CancelFunc) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.Status != TaskStatusPending {
		return fmt.Errorf("cannot start task in status %s", t.Status)
	}

	t.Status = TaskStatusRunning
	now := time.Now()
	t.StartedAt = &now
	t.cancel = cancel
	return nil
}

// Complete 将任务标记为完成
func (t *Task) Complete(output string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.Status = TaskStatusCompleted
	t.Output = output
	now := time.Now()
	t.CompletedAt = &now
}

// Fail 将任务标记为失败
func (t *Task) Fail(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.Status = TaskStatusFailed
	t.Error = err.Error()
	now := time.Now()
	t.CompletedAt = &now
}

// Kill 终止任务
func (t *Task) Kill() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.cancel != nil {
		t.cancel()
	}
	t.Status = TaskStatusKilled
	now := time.Now()
	t.CompletedAt = &now
}

// IsTerminal 任务是否处于终态
func (t *Task) IsTerminal() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Status == TaskStatusCompleted || t.Status == TaskStatusFailed || t.Status == TaskStatusKilled
}

// AppendOutput 追加输出内容
func (t *Task) AppendOutput(content string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Output += content
}

// GetStatus 获取任务状态（线程安全）
func (t *Task) GetStatus() TaskStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Status
}

// Manager 任务管理器领域服务
type Manager struct {
	mu    sync.RWMutex
	tasks map[string]*Task
}

// NewManager 创建任务管理器
func NewManager() *Manager {
	return &Manager{
		tasks: make(map[string]*Task),
	}
}

// Add 添加任务
func (m *Manager) Add(task *Task) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tasks[task.ID] = task
}

// Get 获取任务
func (m *Manager) Get(id string) (*Task, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tasks[id]
	return t, ok
}

// List 列出所有任务（可选状态过滤）
func (m *Manager) List(status ...TaskStatus) []*Task {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statusSet := make(map[TaskStatus]bool)
	for _, s := range status {
		statusSet[s] = true
	}

	var result []*Task
	for _, t := range m.tasks {
		if len(statusSet) == 0 || statusSet[t.GetStatus()] {
			result = append(result, t)
		}
	}
	return result
}

// Remove 移除任务
func (m *Manager) Remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tasks, id)
}

// RunningCount 返回运行中的任务数
func (m *Manager) RunningCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, t := range m.tasks {
		if t.GetStatus() == TaskStatusRunning {
			count++
		}
	}
	return count
}

// Repository 任务持久化接口
type Repository interface {
	Save(task *Task) error
	Load(id string) (*Task, error)
	LoadAll() ([]*Task, error)
	Delete(id string) error
}

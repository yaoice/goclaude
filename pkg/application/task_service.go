package application

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/yaoice/goclaude/pkg/domain/task"
)

// TaskService 任务管理应用服务
type TaskService struct {
	manager *task.Manager
	logger  *slog.Logger
}

// NewTaskService 创建任务服务
func NewTaskService(logger *slog.Logger) *TaskService {
	return &TaskService{
		manager: task.NewManager(),
		logger:  logger,
	}
}

// CreateTask 创建新任务
func (s *TaskService) CreateTask(ctx context.Context, id string, taskType task.TaskType, name string) (*task.Task, error) {
	t := task.NewTask(id, taskType, name)
	s.manager.Add(t)
	s.logger.Debug("创建任务", "id", id, "type", taskType, "name", name)
	return t, nil
}

// StartTask 启动任务
func (s *TaskService) StartTask(ctx context.Context, id string, cancelFn context.CancelFunc) error {
	t, ok := s.manager.Get(id)
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}
	return t.Start(cancelFn)
}

// CompleteTask 完成任务
func (s *TaskService) CompleteTask(id, output string) error {
	t, ok := s.manager.Get(id)
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}
	t.Complete(output)
	s.logger.Debug("任务完成", "id", id)
	return nil
}

// StopTask 停止任务
func (s *TaskService) StopTask(id string) error {
	t, ok := s.manager.Get(id)
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}
	t.Kill()
	s.logger.Debug("任务终止", "id", id)
	return nil
}

// GetTask 获取任务
func (s *TaskService) GetTask(id string) (*task.Task, error) {
	t, ok := s.manager.Get(id)
	if !ok {
		return nil, fmt.Errorf("task %s not found", id)
	}
	return t, nil
}

// ListTasks 列出任务
func (s *TaskService) ListTasks(status ...task.TaskStatus) []*task.Task {
	return s.manager.List(status...)
}

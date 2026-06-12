package application

import (
	"log/slog"
	"time"

	"github.com/yaoice/goclaude/pkg/domain/session"
)

// SessionService 会话管理应用服务
type SessionService struct {
	current *session.Session
	logger  *slog.Logger
}

// NewSessionService 创建会话服务
func NewSessionService(logger *slog.Logger) *SessionService {
	return &SessionService{logger: logger}
}

// CreateSession 创建新会话
func (s *SessionService) CreateSession(workingDir, projectRoot, model string) *session.Session {
	id := generateSessionID()
	sess := session.NewSession(id, workingDir, projectRoot, model)
	s.current = sess
	s.logger.Debug("创建会话", "id", id, "model", model)
	return sess
}

// GetCurrent 获取当前会话
func (s *SessionService) GetCurrent() *session.Session {
	return s.current
}

// RecordUsage 记录使用量
func (s *SessionService) RecordUsage(inputTokens, outputTokens int, costUSD float64) {
	if s.current != nil && s.current.State != nil {
		s.current.State.RecordUsage(inputTokens, outputTokens, costUSD)
	}
}

// generateSessionID 生成会话ID
func generateSessionID() string {
	return time.Now().Format("20060102-150405")
}

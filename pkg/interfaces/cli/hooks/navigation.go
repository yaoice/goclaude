package hooks

import "sync"

// TeamMateState represents a running teammate task
type TeamMateState struct {
	ID     string
	Name   string
	Status string // "running" / "completed" / "failed" / "killed"
}

// SelectionMode 选择模式
type SelectionMode string

const (
	SelectionNone           SelectionMode = "none"
	SelectionSelectingAgent SelectionMode = "selecting-agent"
	SelectionViewingAgent   SelectionMode = "viewing-agent"
)

// NavigationState 导航状态
type NavigationState struct {
	ViewSelectionMode     SelectionMode
	SelectedAgentIndex    int // -1 = leader, 0..n = teammate, n+1 = hide
	ViewingAgentID        string
	ExpandedView          string
	OnOpenBackgroundTasks func()
	OnEnterTeammateView   func(teammateID string)
	OnExitTeammateView    func()
	OnKillTeammate        func(teammateID string)
	OnAbortCurrentWork    func(teammateID string)
}

// BackgroundNavigation 后台任务导航
// Equivalent of useBackgroundTaskNavigation
type BackgroundNavigation struct {
	mu             sync.RWMutex
	state          NavigationState
	teammates      []TeamMateState
	hasNonTeammate bool
	prevCount      int
	onChange       func(NavigationState)
}

// NewBackgroundNavigation 创建后台导航
func NewBackgroundNavigation(onChange func(NavigationState)) *BackgroundNavigation {
	return &BackgroundNavigation{
		state: NavigationState{
			ViewSelectionMode:  SelectionNone,
			SelectedAgentIndex: -1,
		},
		onChange: onChange,
	}
}

// SetTeammates 设置队友列表
func (b *BackgroundNavigation) SetTeammates(teammates []TeamMateState, hasNonTeammateBg bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prevCount = len(b.teammates)
	b.teammates = teammates
	b.hasNonTeammate = hasNonTeammateBg

	// Clamp selection index if teammates are removed
	count := len(teammates)
	if count == 0 && b.prevCount > 0 && b.state.SelectedAgentIndex != -1 {
		b.state.SelectedAgentIndex = -1
		b.state.ViewSelectionMode = SelectionNone
	} else if count > 0 && b.state.SelectedAgentIndex >= count+1 {
		b.state.SelectedAgentIndex = count // "hide" row
	}

	b.prevCount = count
}

// NavigateNext 下一个队友 (Shift+Down)
func (b *BackgroundNavigation) NavigateNext() {
	b.mu.Lock()
	defer b.mu.Unlock()

	n := len(b.teammates)
	if n == 0 {
		if b.hasNonTeammate && b.state.OnOpenBackgroundTasks != nil {
			b.state.OnOpenBackgroundTasks()
		}
		return
	}

	if b.state.ExpandedView != "teammates" {
		b.state.ExpandedView = "teammates"
		b.state.ViewSelectionMode = SelectionSelectingAgent
		b.state.SelectedAgentIndex = -1
		b.notify()
		return
	}

	if b.state.SelectedAgentIndex >= n {
		b.state.SelectedAgentIndex = -1 // wrap to leader
	} else {
		b.state.SelectedAgentIndex++
	}
	b.state.ViewSelectionMode = SelectionSelectingAgent
	b.notify()
}

// NavigatePrev 上一个队友 (Shift+Up)
func (b *BackgroundNavigation) NavigatePrev() {
	b.mu.Lock()
	defer b.mu.Unlock()

	n := len(b.teammates)
	if n == 0 {
		if b.hasNonTeammate && b.state.OnOpenBackgroundTasks != nil {
			b.state.OnOpenBackgroundTasks()
		}
		return
	}

	if b.state.ExpandedView != "teammates" {
		b.state.ExpandedView = "teammates"
		b.state.ViewSelectionMode = SelectionSelectingAgent
		b.state.SelectedAgentIndex = -1
		b.notify()
		return
	}

	if b.state.SelectedAgentIndex <= -1 {
		b.state.SelectedAgentIndex = n // wrap to hide
	} else {
		b.state.SelectedAgentIndex--
	}
	b.state.ViewSelectionMode = SelectionSelectingAgent
	b.notify()
}

// ConfirmSelection 确认选择 (Enter)
func (b *BackgroundNavigation) ConfirmSelection() {
	b.mu.Lock()
	defer b.mu.Unlock()

	n := len(b.teammates)
	if b.state.ViewSelectionMode != SelectionSelectingAgent {
		return
	}

	if b.state.SelectedAgentIndex == -1 {
		if b.state.OnExitTeammateView != nil {
			b.state.OnExitTeammateView()
		}
		return
	}
	if b.state.SelectedAgentIndex >= n {
		// "Hide" row: collapse
		b.state.ExpandedView = "none"
		b.state.ViewSelectionMode = SelectionNone
		b.state.SelectedAgentIndex = -1
		return
	}

	idx := b.state.SelectedAgentIndex
	if idx >= 0 && idx < n && b.state.OnEnterTeammateView != nil {
		b.state.OnEnterTeammateView(b.teammates[idx].ID)
	}
}

// ViewTeammate 查看队友对话 ('f')
func (b *BackgroundNavigation) ViewTeammate() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state.ViewSelectionMode != SelectionSelectingAgent || len(b.teammates) == 0 {
		return
	}

	idx := b.state.SelectedAgentIndex
	if idx >= 0 && idx < len(b.teammates) && b.state.OnEnterTeammateView != nil {
		b.state.OnEnterTeammateView(b.teammates[idx].ID)
	}
}

// KillTeammate 终止队友 ('k')
func (b *BackgroundNavigation) KillTeammate() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state.ViewSelectionMode != SelectionSelectingAgent || b.state.SelectedAgentIndex < 0 {
		return
	}

	idx := b.state.SelectedAgentIndex
	if idx < len(b.teammates) && b.teammates[idx].Status == "running" && b.state.OnKillTeammate != nil {
		b.state.OnKillTeammate(b.teammates[idx].ID)
	}
}

// EscapeViewing 退出查看模式 (Escape in viewing mode)
func (b *BackgroundNavigation) EscapeViewing() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state.ViewSelectionMode == SelectionViewingAgent {
		// Abort current work if running
		id := b.state.ViewingAgentID
		for _, t := range b.teammates {
			if t.ID == id && t.Status == "running" && b.state.OnAbortCurrentWork != nil {
				b.state.OnAbortCurrentWork(id)
				return
			}
		}
		// Not running, exit the view
		if b.state.OnExitTeammateView != nil {
			b.state.OnExitTeammateView()
		}
		return
	}
}

// EscapeSelection 退出选择模式 (Escape in selection mode)
func (b *BackgroundNavigation) EscapeSelection() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state.ViewSelectionMode == SelectionSelectingAgent {
		b.state.ViewSelectionMode = SelectionNone
		b.state.SelectedAgentIndex = -1
		b.notify()
	}
}

// State 返回当前导航状态
func (b *BackgroundNavigation) State() NavigationState {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.state
}

func (b *BackgroundNavigation) notify() {
	if b.onChange != nil {
		b.onChange(b.state)
	}
}

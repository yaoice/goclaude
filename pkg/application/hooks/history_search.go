package hooks

import (
	"strings"
	"sync"
)

// SearchDirection 搜索方向
type SearchDirection int

const (
	SearchForward  SearchDirection = 1
	SearchBackward SearchDirection = -1
)

// HistoryItem 历史记录项
type HistoryItem struct {
	ID        string
	Content   string
	Role      string
	Timestamp int64
}

// HistorySearch 历史搜索器
// Equivalent of useHistorySearch
type HistorySearch struct {
	mu       sync.RWMutex
	items    []HistoryItem
	query    string
	matchIdx int
	onChange func(state HistorySearchState)
}

// HistorySearchState 搜索状态
type HistorySearchState struct {
	Query      string
	MatchIndex int
	MatchTotal int
	IsActive   bool
}

// NewHistorySearch 创建历史搜索器
func NewHistorySearch() *HistorySearch {
	return &HistorySearch{}
}

// SetItems 设置历史项
func (h *HistorySearch) SetItems(items []HistoryItem) {
	h.mu.Lock()
	h.items = items
	h.mu.Unlock()
}

// OnChange 注册状态变更回调
func (h *HistorySearch) OnChange(fn func(HistorySearchState)) {
	h.mu.Lock()
	h.onChange = fn
	h.mu.Unlock()
}

// Search 执行搜索
func (h *HistorySearch) Search(query string) HistorySearchState {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.query = query
	matchCount := 0
	firstMatch := -1

	if query != "" {
		lowerQuery := strings.ToLower(query)
		for i, item := range h.items {
			if strings.Contains(strings.ToLower(item.Content), lowerQuery) {
				if firstMatch < 0 {
					firstMatch = i
				}
				matchCount++
			}
		}
	}

	state := HistorySearchState{
		Query:      query,
		MatchIndex: firstMatch,
		MatchTotal: matchCount,
		IsActive:   query != "" && matchCount > 0,
	}
	h.matchIdx = 0

	if h.onChange != nil {
		h.onChange(state)
	}
	return state
}

// Next 跳转到下一个匹配
func (h *HistorySearch) Next() HistorySearchState {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.query == "" {
		return HistorySearchState{}
	}

	matches := h.findMatches()
	if len(matches) == 0 {
		return HistorySearchState{}
	}

	h.matchIdx = (h.matchIdx + 1) % len(matches)
	state := HistorySearchState{
		Query:      h.query,
		MatchIndex: matches[h.matchIdx],
		MatchTotal: len(matches),
		IsActive:   true,
	}
	if h.onChange != nil {
		h.onChange(state)
	}
	return state
}

// Prev 跳转到上一个匹配
func (h *HistorySearch) Prev() HistorySearchState {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.query == "" {
		return HistorySearchState{}
	}

	matches := h.findMatches()
	if len(matches) == 0 {
		return HistorySearchState{}
	}

	h.matchIdx--
	if h.matchIdx < 0 {
		h.matchIdx = len(matches) - 1
	}
	state := HistorySearchState{
		Query:      h.query,
		MatchIndex: matches[h.matchIdx],
		MatchTotal: len(matches),
		IsActive:   true,
	}
	if h.onChange != nil {
		h.onChange(state)
	}
	return state
}

func (h *HistorySearch) findMatches() []int {
	var matches []int
	lowerQuery := strings.ToLower(h.query)
	for i, item := range h.items {
		if strings.Contains(strings.ToLower(item.Content), lowerQuery) {
			matches = append(matches, i)
		}
	}
	return matches
}

// Clear 清空搜索
func (h *HistorySearch) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.query = ""
	h.matchIdx = 0
	if h.onChange != nil {
		h.onChange(HistorySearchState{})
	}
}

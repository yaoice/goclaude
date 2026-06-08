// Package hooks — diff 数据钩子
//
// 对齐 src/hooks/ 中的 diff 钩子实现：
//   - DiffManager: 等价于 useDiffData.ts 和 useDiffInIDE.ts
//
// 提供 diff 数据的增删改查和状态管理。

package hooks

import (
	"sync"
)

// DiffData diff 数据结构（对齐 useDiffData.ts）
type DiffData struct {
	ID        string
	FilePath  string
	Original  string
	Modified  string
	PatchData string
	Status    string // "pending", "applied", "rejected"
	Timestamp int64
}

// DiffManager diff 数据管理器
type DiffManager struct {
	mu       sync.RWMutex
	diffs    map[string]*DiffData
	onChange func([]DiffData)
}

// NewDiffManager 创建 diff 管理器
func NewDiffManager() *DiffManager {
	return &DiffManager{diffs: make(map[string]*DiffData)}
}

// OnChange 设置变更回调
func (dm *DiffManager) OnChange(fn func([]DiffData)) { dm.onChange = fn }

// Add 添加 diff
func (dm *DiffManager) Add(d *DiffData) {
	dm.mu.Lock()
	dm.diffs[d.ID] = d
	dm.mu.Unlock()
	if dm.onChange != nil {
		dm.onChange(dm.List())
	}
}

// Get 获取指定 diff
func (dm *DiffManager) Get(id string) (*DiffData, bool) {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	d, ok := dm.diffs[id]
	return d, ok
}

// UpdateStatus 更新 diff 状态
func (dm *DiffManager) UpdateStatus(id string, status string) {
	dm.mu.Lock()
	if d, ok := dm.diffs[id]; ok {
		d.Status = status
	}
	dm.mu.Unlock()
	if dm.onChange != nil {
		dm.onChange(dm.List())
	}
}

// List 列出所有 diff
func (dm *DiffManager) List() []DiffData {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	result := make([]DiffData, 0, len(dm.diffs))
	for _, d := range dm.diffs {
		result = append(result, *d)
	}
	return result
}

// Remove 移除 diff
func (dm *DiffManager) Remove(id string) {
	dm.mu.Lock()
	delete(dm.diffs, id)
	dm.mu.Unlock()
	if dm.onChange != nil {
		dm.onChange(dm.List())
	}
}

// Clear 清空
func (dm *DiffManager) Clear() {
	dm.mu.Lock()
	dm.diffs = make(map[string]*DiffData)
	dm.mu.Unlock()
}

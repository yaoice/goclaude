package hooks

import "sync"

// ============================================================
// useQueue — 泛型队列
// 对齐 src/hooks/useQueue.ts
//
// 核心逻辑：线程安全的泛型队列，支持 subscribe 模式。
// ============================================================

// Queue 线程安全队列
type Queue[T any] struct {
	items    []T
	mu       sync.RWMutex
	onChange func()
}

// NewQueue 创建队列
func NewQueue[T any]() *Queue[T] {
	return &Queue[T]{}
}

// Enqueue 入队
func (q *Queue[T]) Enqueue(item T) {
	q.mu.Lock()
	q.items = append(q.items, item)
	cb := q.onChange
	q.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// Dequeue 出队
func (q *Queue[T]) Dequeue() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		var zero T
		return zero, false
	}
	item := q.items[0]
	q.items = q.items[1:]
	return item, true
}

// Peek 查看队首
func (q *Queue[T]) Peek() (T, bool) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if len(q.items) == 0 {
		var zero T
		return zero, false
	}
	return q.items[0], true
}

// Len 队列长度
func (q *Queue[T]) Len() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.items)
}

// Items 返回队列快照（只读副本）
func (q *Queue[T]) Items() []T {
	q.mu.RLock()
	defer q.mu.RUnlock()
	result := make([]T, len(q.items))
	copy(result, q.items)
	return result
}

// Clear 清空队列
func (q *Queue[T]) Clear() {
	q.mu.Lock()
	q.items = nil
	q.mu.Unlock()
}

// OnChange 注册变更回调
func (q *Queue[T]) OnChange(fn func()) {
	q.mu.Lock()
	q.onChange = fn
	q.mu.Unlock()
}

// ============================================================
// useCommandQueue — 命令队列订阅
// 对齐 src/hooks/useCommandQueue.ts
//
// 核心逻辑：通过 useSyncExternalStore 订阅 command queue，
// Go 版本通过回调机制实现订阅。
// ============================================================

// CommandQueue 命令队列
type CommandQueue struct {
	queue    *Queue[string]
	mu       sync.RWMutex
	subs     map[int]chan []string
	nextID   int
}

var globalCommandQueue = NewCommandQueue()

// NewCommandQueue 创建命令队列
func NewCommandQueue() *CommandQueue {
	return &CommandQueue{
		queue: NewQueue[string](),
		subs:  make(map[int]chan []string),
	}
}

// GlobalCommandQueue 全局命令队列单例
func GlobalCommandQueue() *CommandQueue {
	return globalCommandQueue
}

// Push 推入命令
func (cq *CommandQueue) Push(cmd string) {
	cq.queue.Enqueue(cmd)
	cq.notify()
}

// Pop 弹出命令
func (cq *CommandQueue) Pop() (string, bool) {
	cmd, ok := cq.queue.Dequeue()
	if ok {
		cq.notify()
	}
	return cmd, ok
}

// Snapshot 返回当前队列快照
func (cq *CommandQueue) Snapshot() []string {
	return cq.queue.Items()
}

// Subscribe 订阅队列变更，返回 unsubscribe 函数
func (cq *CommandQueue) Subscribe(ch chan []string) func() {
	cq.mu.Lock()
	id := cq.nextID
	cq.nextID++
	cq.subs[id] = ch
	cq.mu.Unlock()

	// 立即发送当前快照
	select {
	case ch <- cq.Snapshot():
	default:
	}

	return func() {
		cq.mu.Lock()
		delete(cq.subs, id)
		cq.mu.Unlock()
	}
}

func (cq *CommandQueue) notify() {
	snapshot := cq.Snapshot()
	cq.mu.RLock()
	defer cq.mu.RUnlock()
	for _, ch := range cq.subs {
		select {
		case ch <- snapshot:
		default:
		}
	}
}

// ============================================================
// InputBuffer Hook — 输入缓冲区管理
// 对齐 src/hooks/useInputBuffer.ts
//
// 核心逻辑：管理终端输入缓冲区，支持 push/peek/pop/undo 操作。
// ============================================================

// InputBuffer 输入缓冲区
type InputBuffer struct {
	buffer []rune
	undo   []rune
	mu     sync.RWMutex
}

// NewInputBuffer 创建输入缓冲区
func NewInputBuffer() *InputBuffer {
	return &InputBuffer{}
}

// Push 追加字符到缓冲区
func (ib *InputBuffer) Push(ch rune) {
	ib.mu.Lock()
	ib.buffer = append(ib.buffer, ch)
	ib.undo = nil // 新输入清空 undo 栈
	ib.mu.Unlock()
}

// Pop 移除并返回最后一个字符
func (ib *InputBuffer) Pop() (rune, bool) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	if len(ib.buffer) == 0 {
		return 0, false
	}
	ch := ib.buffer[len(ib.buffer)-1]
	ib.buffer = ib.buffer[:len(ib.buffer)-1]
	ib.undo = append(ib.undo, ch)
	return ch, true
}

// Undo 恢复最近移除的字符
func (ib *InputBuffer) Undo() (rune, bool) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	if len(ib.undo) == 0 {
		return 0, false
	}
	ch := ib.undo[len(ib.undo)-1]
	ib.undo = ib.undo[:len(ib.undo)-1]
	ib.buffer = append(ib.buffer, ch)
	return ch, true
}

// String 返回缓冲区内容
func (ib *InputBuffer) String() string {
	ib.mu.RLock()
	defer ib.mu.RUnlock()
	return string(ib.buffer)
}

// Len 缓冲区长度
func (ib *InputBuffer) Len() int {
	ib.mu.RLock()
	defer ib.mu.RUnlock()
	return len(ib.buffer)
}

// Clear 清空缓冲区
func (ib *InputBuffer) Clear() {
	ib.mu.Lock()
	ib.buffer = nil
	ib.undo = nil
	ib.mu.Unlock()
}

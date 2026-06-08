// Package hooks 提供 Go 版本的 React Custom Hooks 等价实现
//
// 将 src/hooks/ 中的 React hooks 适配为 Go 惯用模式：
//   - useState    → 结构体字段 + sync.RWMutex 保护
//   - useEffect   → Init / Start / Close 生命周期方法
//   - useCallback → 结构体方法
//   - useMemo     → sync.Once 或 lazy init
//   - useRef      → 指针字段
//
// 每个 hook 对应一个独立的 Go 结构体，保留核心逻辑、参数和返回值结构。
package hooks

// This file serves as the package documentation.
// Individual hook implementations are in separate files:
//   - timer.go     (useElapsedTime, useTimeout, useDoublePress, useBlink)
//   - queue.go     (useQueue, useCommandQueue)
//   - lifecycle.go (useAfterFirstRender, useFileHistorySnapshotInit)
//   - config.go    (useDynamicConfig)

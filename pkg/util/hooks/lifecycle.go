package hooks

// ============================================================
// useDynamicConfig — 动态配置 Hook
// 对齐 src/hooks/useDynamicConfig.ts
//
// 核心逻辑：初始化时返回 defaultValue，配置加载完成后更新为实际值。
// Go 版本通过回调或 channel 通知更新。
// ============================================================

// DynamicConfig 动态配置获取器
// T: 配置值类型
type DynamicConfig[T any] struct {
	configName   string
	defaultValue T
	value        T
	loaded       bool
	fetcher      func(configName string, defaultValue T) (T, error)
	onUpdate     func(T)
}

// NewDynamicConfig 创建动态配置获取器
func NewDynamicConfig[T any](configName string, defaultValue T, fetcher func(string, T) (T, error)) *DynamicConfig[T] {
	return &DynamicConfig[T]{
		configName:   configName,
		defaultValue: defaultValue,
		value:        defaultValue,
		fetcher:      fetcher,
	}
}

// OnUpdate 设置更新回调
func (dc *DynamicConfig[T]) OnUpdate(fn func(T)) {
	dc.onUpdate = fn
}

// Load 加载配置（首次调用时异步获取，后续返回缓存值）
func (dc *DynamicConfig[T]) Load() (T, error) {
	if dc.loaded {
		return dc.value, nil
	}

	if dc.fetcher == nil {
		dc.loaded = true
		return dc.defaultValue, nil
	}

	v, err := dc.fetcher(dc.configName, dc.defaultValue)
	if err != nil {
		return dc.defaultValue, err
	}

	dc.loaded = true
	dc.value = v
	if dc.onUpdate != nil {
		dc.onUpdate(v)
	}
	return v, nil
}

// LoadAsync 异步加载配置，通过 channel 返回结果
func (dc *DynamicConfig[T]) LoadAsync() <-chan struct {
	Value T
	Error error
} {
	resultCh := make(chan struct {
		Value T
		Error error
	}, 1)

	go func() {
		v, err := dc.Load()
		resultCh <- struct {
			Value T
			Error error
		}{v, err}
		close(resultCh)
	}()

	return resultCh
}

// Value 获取当前值（可能为默认值，如果尚未加载）
func (dc *DynamicConfig[T]) Value() T {
	return dc.value
}

// IsLoaded 是否已加载
func (dc *DynamicConfig[T]) IsLoaded() bool {
	return dc.loaded
}

// ============================================================
// useAfterFirstRender — 首次渲染后 Hook
// 对齐 src/hooks/useAfterFirstRender.ts
//
// 核心逻辑：首次"渲染"（启动）后执行一次性操作。
// 在 ANT_USER 环境中，检查 CLAUDE_CODE_EXIT_AFTER_FIRST_RENDER 环境变量。
// ============================================================

// AfterFirstRender 首次渲染后回调
type AfterFirstRender struct {
	onAfter func()
}

// NewAfterFirstRender 创建
func NewAfterFirstRender(onAfter func()) *AfterFirstRender {
	return &AfterFirstRender{onAfter: onAfter}
}

// Run 执行首次后回调（仅一次）
func (a *AfterFirstRender) Run() {
	if a.onAfter != nil {
		a.onAfter()
	}
}

// ============================================================
// useFileHistorySnapshotInit — 文件历史快照初始化
// 对齐 src/hooks/useFileHistorySnapshotInit.ts
//
// 核心逻辑：在 fileHistory 启用时，用初始快照恢复文件历史状态。
// Go 版本通过显式调用 Init 完成。
// ============================================================

// FileHistorySnapshot 文件历史快照
type FileHistorySnapshot struct {
	FilePath  string
	Content   string
	Timestamp int64
}

// FileHistoryState 文件历史状态
type FileHistoryState struct {
	Snapshots []FileHistorySnapshot
	Enabled   bool
}

// FileHistorySnapshotInit 文件历史初始化器
type FileHistorySnapshotInit struct {
	initialized bool
}

// NewFileHistorySnapshotInit 创建
func NewFileHistorySnapshotInit() *FileHistorySnapshotInit {
	return &FileHistorySnapshotInit{}
}

// Init 从初始快照恢复状态
// enabled: 是否启用文件历史
// snapshots: 初始快照列表
// onStateUpdate: 状态更新回调
func (f *FileHistorySnapshotInit) Init(
	enabled bool,
	snapshots []FileHistorySnapshot,
	onStateUpdate func(FileHistoryState),
) {
	if !enabled || f.initialized {
		return
	}
	f.initialized = true
	if snapshots != nil && onStateUpdate != nil {
		onStateUpdate(FileHistoryState{
			Snapshots: snapshots,
			Enabled:   enabled,
		})
	}
}

// Reset 重置初始化状态
func (f *FileHistorySnapshotInit) Reset() {
	f.initialized = false
}

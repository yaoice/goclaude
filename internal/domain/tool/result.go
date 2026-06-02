package tool

// Result 工具执行结果
type Result struct {
	// Content 结果内容文本
	Content string
	// IsError 是否为错误结果
	IsError bool
	// Metadata 附加元数据
	Metadata map[string]interface{}
}

// NewResult 创建成功结果
func NewResult(content string) *Result {
	return &Result{
		Content: content,
		IsError: false,
	}
}

// NewErrorResult 创建错误结果
func NewErrorResult(content string) *Result {
	return &Result{
		Content: content,
		IsError: true,
	}
}

// WithMetadata 添加元数据
func (r *Result) WithMetadata(key string, value interface{}) *Result {
	if r.Metadata == nil {
		r.Metadata = make(map[string]interface{})
	}
	r.Metadata[key] = value
	return r
}

// MaxResultSize 工具结果最大字符数（超限需持久化到磁盘）
const MaxResultSize = 30000

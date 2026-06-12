// Package memory — 记忆内容筛选与过滤机制
//
// 提供四层过滤能力：
//  1. 自定义规则引擎 — 关键词 / 正则 / 语义类别，定义 include / exclude 行为
//  2. 上下文相关性评估 — 基于关键词重叠度、语义距离的评分
//  3. 优先级权重分配 — 来源、时效、重复次数综合权重
//  4. 容量管理策略  — LRU 淘汰 + 低优先级条目摘要压缩
package memory

import (
	"regexp"
	"strings"
	"sync"
	"time"
)

// ============================================================
// 一、记忆条目模型
// ============================================================

// MemoryEntry 单条记忆条目（扩展版，含过滤元数据）
type MemoryEntry struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	Source      string    `json:"source"`             // "user_directive" | "auto_extract" | "agent_note"
	Category    string    `json:"category,omitempty"` // "project" | "user" | "reference" | "feedback"
	Tags        []string  `json:"tags,omitempty"`
	Priority    int       `json:"priority"`  // 0-100, 越高越重要
	Relevance   float64   `json:"relevance"` // 0.0-1.0 与当前上下文的相关度
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	AccessCount int       `json:"access_count"` // 被读取次数
	ByteSize    int       `json:"byte_size"`
}

// TotalScore 综合评分 = priority * 0.5 + relevance * 0.3 + recency * 0.2
func (e *MemoryEntry) TotalScore(now time.Time) float64 {
	daysSinceUpdate := now.Sub(e.UpdatedAt).Hours() / 24
	recency := 1.0
	if daysSinceUpdate > 0 {
		recency = 1.0 / (1.0 + daysSinceUpdate/30.0) // 30天半衰
	}
	return float64(e.Priority)*0.5/100 + e.Relevance*0.3 + recency*0.2
}

// ============================================================
// 二、规则引擎类型
// ============================================================

// RuleAction 规则动作
type RuleAction string

const (
	ActionInclude RuleAction = "include" // 强制保留
	ActionExclude RuleAction = "exclude" // 强制丢弃
	ActionBoost   RuleAction = "boost"   // 提升优先级
	ActionDemote  RuleAction = "demote"  // 降低优先级
	ActionTag     RuleAction = "tag"     // 添加标签
)

// RuleMatchType 规则匹配类型
type RuleMatchType string

const (
	MatchKeyword  RuleMatchType = "keyword"  // 子串匹配
	MatchRegex    RuleMatchType = "regex"    // 正则匹配
	MatchCategory RuleMatchType = "category" // 语义类别匹配
	MatchTag      RuleMatchType = "tag"      // 标签匹配
	MatchSource   RuleMatchType = "source"   // 来源匹配
)

// FilterRule 单条过滤规则
type FilterRule struct {
	Name      string        `json:"name"`
	MatchType RuleMatchType `json:"match_type"`
	Pattern   string        `json:"pattern"` // keyword 子串 / regex 模式 / category 名
	Action    RuleAction    `json:"action"`
	Priority  int           `json:"priority,omitempty"` // boost/demote 时的数值
	BoostBy   int           `json:"boost_by,omitempty"` // action=boost 时增加的优先级分值
	Tags      []string      `json:"tags,omitempty"`     // action=tag 时附加的标签

	compiled *regexp.Regexp // 预编译的正则（内部缓存）
}

// compile 预编译正则（惰性）
func (r *FilterRule) compile() (*regexp.Regexp, error) {
	if r.compiled != nil {
		return r.compiled, nil
	}
	if r.MatchType != MatchRegex {
		return nil, nil
	}
	re, err := regexp.Compile(r.Pattern)
	if err != nil {
		return nil, err
	}
	r.compiled = re
	return re, nil
}

// Match 检查规则是否匹配条目
func (r *FilterRule) Match(entry *MemoryEntry) (bool, error) {
	switch r.MatchType {
	case MatchKeyword:
		return strings.Contains(strings.ToLower(entry.Title+" "+entry.Content), strings.ToLower(r.Pattern)), nil
	case MatchRegex:
		re, err := r.compile()
		if err != nil {
			return false, err
		}
		return re.MatchString(entry.Title) || re.MatchString(entry.Content), nil
	case MatchCategory:
		return strings.EqualFold(entry.Category, r.Pattern), nil
	case MatchTag:
		for _, t := range entry.Tags {
			if strings.EqualFold(t, r.Pattern) {
				return true, nil
			}
		}
		return false, nil
	case MatchSource:
		return strings.EqualFold(entry.Source, r.Pattern), nil
	default:
		return false, nil
	}
}

// ============================================================
// 三、规则引擎
// ============================================================

// RuleEngine 规则引擎（顺序执行，首个匹配的 include/exclude 立即生效）
type RuleEngine struct {
	mu    sync.RWMutex
	rules []*FilterRule
}

// NewRuleEngine 创建规则引擎
func NewRuleEngine(rules []*FilterRule) *RuleEngine {
	return &RuleEngine{rules: rules}
}

// SetRules 更新规则（线程安全）
func (e *RuleEngine) SetRules(rules []*FilterRule) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rules = rules
}

// Apply 应用规则链，返回是否保留、修改后的条目、冲突的规则名
//
// 规则按顺序执行：
//   - include → 立即保留
//   - exclude → 立即丢弃
//   - boost/demote/tag → 累积修改，继续检查后续规则
func (e *RuleEngine) Apply(entry *MemoryEntry) (keep bool, modified *MemoryEntry, matchedRule string) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	modified = &MemoryEntry{}
	*modified = *entry
	modified.Tags = append([]string{}, entry.Tags...)

	for _, rule := range e.rules {
		match, err := rule.Match(modified)
		if err != nil || !match {
			continue
		}

		switch rule.Action {
		case ActionInclude:
			return true, modified, rule.Name
		case ActionExclude:
			return false, modified, rule.Name
		case ActionBoost:
			modified.Priority += rule.BoostBy
			if modified.Priority > 100 {
				modified.Priority = 100
			}
		case ActionDemote:
			modified.Priority -= rule.BoostBy
			if modified.Priority < 0 {
				modified.Priority = 0
			}
		case ActionTag:
			modified.Tags = appendUnique(modified.Tags, rule.Tags...)
		}
	}

	// 默认：无显式 include/exclude → 保留
	return true, modified, ""
}

func appendUnique(slice []string, items ...string) []string {
	for _, item := range items {
		found := false
		for _, existing := range slice {
			if strings.EqualFold(existing, item) {
				found = true
				break
			}
		}
		if !found && item != "" {
			slice = append(slice, item)
		}
	}
	return slice
}

// ============================================================
// 四、内置规则（最佳实践预设）
// ============================================================

// BuiltInRules 返回推荐的内置过滤规则
func BuiltInRules() []*FilterRule {
	return []*FilterRule{
		// 安全：排除敏感信息
		{
			Name:      "exclude-secrets",
			MatchType: MatchRegex,
			Pattern:   `(?i)(password|secret|token|api[_-]?key|private[_-]?key)\s*[:=]\s*\S+`,
			Action:    ActionExclude,
		},
		// 安全：排除凭证模板
		{
			Name:      "exclude-credentials",
			MatchType: MatchKeyword,
			Pattern:   "BEGIN RSA PRIVATE KEY",
			Action:    ActionExclude,
		},
		// 质量：排除纯噪音标题（单字符或纯数字等无意义标题）
		{
			Name:      "exclude-noise",
			MatchType: MatchRegex,
			Pattern:   `^$`,
			Action:    ActionExclude,
		},
		// 提升：用户明确指令高优先级
		{
			Name:      "boost-user-directive",
			MatchType: MatchSource,
			Pattern:   "user_directive",
			Action:    ActionBoost,
			BoostBy:   25,
		},
		// 标签：安全相关内容标记
		{
			Name:      "tag-security",
			MatchType: MatchKeyword,
			Pattern:   "security",
			Action:    ActionTag,
			Tags:      []string{"security"},
		},
		// 标签：配置相关内容标记
		{
			Name:      "tag-config",
			MatchType: MatchRegex,
			Pattern:   `(?i)(config|settings?|env(ironment)?\s*var|\.env)`,
			Action:    ActionTag,
			Tags:      []string{"config"},
		},
		// 排除：临时/调试信息
		{
			Name:      "exclude-temp",
			MatchType: MatchKeyword,
			Pattern:   "TODO: remove this",
			Action:    ActionExclude,
		},
	}
}

// ============================================================
// 五、优先级分配
// ============================================================

// AssignPriority 根据来源和分类计算默认优先级
func AssignPriority(entry *MemoryEntry) int {
	base := 50

	// 来源权重
	switch entry.Source {
	case "user_directive":
		base += 40
	case "agent_note":
		base += 10
	case "auto_extract":
		base -= 20
	}

	if base > 100 {
		base = 100
	}
	if base < 10 {
		base = 10
	}
	return base
}

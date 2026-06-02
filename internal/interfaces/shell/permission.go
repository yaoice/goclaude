package shell

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

// PermissionDialog 工具权限弹窗
//
// 与 src `components/Permission/PermissionRequest.tsx` 对齐的简化版。
// 在原始模式下打开一个内联弹层（占据若干行）：
//
//	╭─ Permission required ─────────────────╮
//	│ → <tool_name>                          │
//	│   <input preview, truncated>           │
//	│ reason: <why>                          │
//	│                                        │
//	│ [a] Allow once                         │
//	│ [s] Allow always (this session)        │
//	│ [d] Deny                               │
//	╰────────────────────────────────────────╯
//
// 行为：
//   - a / Enter / y       → allow once
//   - s                   → allow always for this tool name
//   - d / Esc / Ctrl+C / n → deny
//   - 期间不接受其它键
//
// "Always" 集合保存在 REPL 内存中，REPL 退出即失效；
// 与 src 的"会话级 alwaysAllow"语义一致，不持久化避免危险扩散。
type PermissionDialog struct {
	mu          sync.Mutex
	alwaysAllow map[string]struct{} // tool name set
}

// NewPermissionDialog 构造一个权限弹窗
func NewPermissionDialog() *PermissionDialog {
	return &PermissionDialog{alwaysAllow: map[string]struct{}{}}
}

// IsAlwaysAllowed 返回该工具是否被本会话标记为"始终放行"
func (p *PermissionDialog) IsAlwaysAllowed(tool string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.alwaysAllow[tool]
	return ok
}

// addAlways 把工具加入永久放行集合
func (p *PermissionDialog) addAlways(tool string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.alwaysAllow[tool] = struct{}{}
}

// Ask 在 shell 中弹窗询问用户是否允许工具调用
//
// repl: 用于暂停 raw 模式 / 暂停编辑器；ctx 用于取消（生成中 SIGINT 会触发）
//
// 返回值：
//   - allow=true  → 允许执行
//   - allow=false → 拒绝（工具会得到 "permission denied"）
func (p *PermissionDialog) Ask(ctx context.Context, repl *REPL, toolName string, input any, reason string) (bool, error) {
	// 先看 alwaysAllow
	if p.IsAlwaysAllowed(toolName) {
		return true, nil
	}

	repl.pauseInputMu.Lock()
	defer repl.pauseInputMu.Unlock()

	// 构造预览
	preview := previewToolInput(input)

	repl.writeOut("\r\n")
	repl.writeOut(repl.colorize("╭─ Permission required ─────────────────╮\r\n", colorYellow))
	repl.writeOut(repl.colorize("│ ", colorYellow) + repl.colorize(repl.gl().toolCall+toolName, colorAccent) + "\r\n")
	if preview != "" {
		repl.writeOut(repl.colorize("│   ", colorYellow) + repl.colorize(preview, colorDim) + "\r\n")
	}
	if reason != "" {
		repl.writeOut(repl.colorize("│ ", colorYellow) + repl.colorize("reason: "+reason, colorDim) + "\r\n")
	}
	repl.writeOut(repl.colorize("│\r\n", colorYellow))
	repl.writeOut(repl.colorize("│ ", colorYellow) + repl.colorize("[a]", colorGreen) + " Allow once\r\n")
	repl.writeOut(repl.colorize("│ ", colorYellow) + repl.colorize("[s]", colorGreen) + " Allow always (this session)\r\n")
	repl.writeOut(repl.colorize("│ ", colorYellow) + repl.colorize("[d]", colorRed) + " Deny\r\n")
	repl.writeOut(repl.colorize("╰────────────────────────────────────────╯\r\n", colorYellow))
	repl.writeOut(repl.colorize("> ", colorAccent))

	// 在 raw 模式下读单字节（不需要 cooked），所以不切 termios
	type res struct {
		choice byte
		err    error
	}
	ch := make(chan res, 1)
	go func() {
		for {
			b, err := repl.Term.ReadByte()
			if err != nil {
				ch <- res{err: err}
				return
			}
			switch b {
			case 'a', 'A', 'y', 'Y', '\r', '\n':
				ch <- res{choice: 'a'}
				return
			case 's', 'S':
				ch <- res{choice: 's'}
				return
			case 'd', 'D', 'n', 'N', 0x1b /*ESC*/, 0x03 /*Ctrl+C*/ :
				ch <- res{choice: 'd'}
				return
			}
			// 其它按键忽略（不返回）
		}
	}()

	select {
	case <-ctx.Done():
		repl.writeOut("\r\n")
		return false, ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return false, r.err
		}
		switch r.choice {
		case 'a':
			repl.writeOut("\r\n" + repl.colorize(repl.gl().result+"allow once", colorGreen) + "\r\n")
			return true, nil
		case 's':
			p.addAlways(toolName)
			repl.writeOut("\r\n" + repl.colorize(fmt.Sprintf("%sallow always (%s)", repl.gl().result, toolName), colorGreen) + "\r\n")
			return true, nil
		case 'd':
			repl.writeOut("\r\n" + repl.colorize(repl.gl().result+"deny", colorRed) + "\r\n")
			return false, nil
		}
	}
	return false, nil
}

// previewToolInput 把工具输入压缩为 1 行预览
func previewToolInput(input any) string {
	if input == nil {
		return ""
	}
	// 优先尝试 JSON
	if b, err := json.Marshal(input); err == nil {
		s := string(b)
		s = strings.ReplaceAll(s, "\n", " ")
		if r := []rune(s); len(r) > 100 {
			s = string(r[:99]) + "…"
		}
		return s
	}
	return fmt.Sprintf("%v", input)
}

// AlwaysAllowedNames 返回当前会话已 always-allow 的工具名列表（调试 / 状态行用）
func (p *PermissionDialog) AlwaysAllowedNames() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.alwaysAllow))
	for k := range p.alwaysAllow {
		out = append(out, k)
	}
	return out
}

// 兜底引用 os 防止 import unused（debug 时可去除）
var _ = os.Stdout

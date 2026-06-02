package shell

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LaunchExternalEditor 在 $EDITOR / $VISUAL 中打开当前文本，编辑后返回新文本
//
// 行为：
//   - 把 current 写入临时文件（基于工作目录的 $TMPDIR/.claude_edit_<pid>.md）
//   - 优先读 $VISUAL → $EDITOR → vi 兜底
//   - 暂离 raw 模式 + 关闭 bracketed paste；进程前台运行（继承 stdin/stdout/stderr）
//   - 编辑器退出后，读回文件作为新文本
//   - 任何环节失败都返回 (current, false)，REPL 主循环按"未替换"处理
//
// 与 src `chat:externalEditor` 行为对齐：用户在编辑器里写 markdown，关闭即采用。
func (r *REPL) LaunchExternalEditor(current string) (string, bool) {
	editor := firstNonEmpty(os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")
	tmpDir := os.TempDir()
	f, err := os.CreateTemp(tmpDir, ".claude_edit_*.md")
	if err != nil {
		r.writeOut(r.colorize(fmt.Sprintf("无法创建临时文件: %v\r\n", err), colorRed))
		return current, false
	}
	tmpPath := f.Name()
	defer os.Remove(tmpPath)
	if _, err := f.WriteString(current); err != nil {
		_ = f.Close()
		r.writeOut(r.colorize(fmt.Sprintf("写入临时文件失败: %v\r\n", err), colorRed))
		return current, false
	}
	_ = f.Close()

	// 暂停 shell 输入并退出 raw + bracketed paste
	r.pauseInputMu.Lock()
	defer r.pauseInputMu.Unlock()

	r.Editor.EnableBracketedPaste(false)
	_ = r.Term.LeaveRaw()
	defer func() {
		_ = r.Term.EnterRaw()
		r.Editor.EnableBracketedPaste(true)
	}()

	// 拆 editor 命令（支持带参数，如 "code --wait"）
	parts := strings.Fields(editor)
	if len(parts) == 0 {
		parts = []string{"vi"}
	}
	args := append(parts[1:], tmpPath)
	cmd := exec.Command(parts[0], args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = r.WorkDir

	r.writeOut(r.colorize(fmt.Sprintf("%s%s %s\r\n", r.gl().toolCall, filepath.Base(parts[0]), tmpPath), colorAccent))
	if err := cmd.Run(); err != nil {
		r.writeOut(r.colorize(fmt.Sprintf("外部编辑器退出错误: %v\r\n", err), colorYellow))
		return current, false
	}

	// 读回
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		r.writeOut(r.colorize(fmt.Sprintf("读回临时文件失败: %v\r\n", err), colorRed))
		return current, false
	}
	newText := string(data)
	// 去掉尾部 \n（多数编辑器自动加的）；保留中间换行
	newText = strings.TrimRight(newText, "\n\r")
	return newText, true
}

// firstNonEmpty 返回第一个非空字符串
func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

package shell

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// 本文件聚合 REPL 与用户/系统的低层交互：`!` bash 模式、工具权限弹窗、
// ask_user 征询、SIGINT/SIGWINCH 信号语义。从 repl.go 拆出，逻辑不变。

// stdinCancelLoop 在生成期间监听 stdin 的 0x03 字节（Ctrl+C），实现实时取消。
//
// 背景：终端原始模式（MakeRaw 清除 ISIG）下，Ctrl+C 产生字节 0x03 而非 SIGINT。
// 当主循环阻塞在 runOnce（LLM 生成中）时，ReadLine 未运行，没人消费 stdin。
// 此 goroutine 在生成中持续读 stdin：
//   - 读到 0x03 → 调用 activeCancel 中止当前查询
//   - 读到其他字节 → 忽略（生成完毕后 ReadLine 恢复时不再读到已消耗的字节）
//   - ctx 取消或 done 关闭 → 退出
//
// 此 goroutine 与 ReadLine 通过 generating 标志协调 stdin 所有权：
//
//	generating=true  → stdinCancelLoop 持有 stdin
//	generating=false → ReadLine 持有 stdin（stdinCancelLoop 主动休眠让出）
func (r *REPL) stdinCancelLoop(ctx context.Context, done <-chan struct{}) {
	buf := make([]byte, 1)
	for {
		// 优先检查退出条件
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		default:
		}

		if !r.generating.Load() {
			// 空闲态：让 ReadLine 独占 stdin，短暂睡眠后重试
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}

		// 生成中：阻塞读一个字节
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			// stdin 关闭（EOF）或出错：退出 goroutine，不影响主流程
			return
		}
		if buf[0] == 0x03 { // ETX = Ctrl+C
			// 再次确认仍在生成中（避免竞态：Read 返回时 runOnce 刚好结束）
			if r.generating.Load() {
				r.mu.Lock()
				cancel := r.activeCancel
				r.mu.Unlock()
				if cancel != nil {
					cancel()
				}
			}
		}
		// 非 0x03：字节被消耗，这是可接受的代价
		// （生成过程中用户的其它按键输入无实际意义）
	}
}

// runBash 在本地执行一段 shell 命令（`!cmd` 模式）
//
// 行为对齐 src `processBashCommand`：
//   - 不发给 LLM
//   - stdout/stderr 合并显示
//   - 退出码 0 显示绿色 ⎿ exit 0；否则红色 ⎿ exit N
//   - 暂时离开原始模式，让交互式命令能正常工作（vim/less 等）
func (r *REPL) runBash(parent context.Context, cmdline string) {
	if strings.TrimSpace(cmdline) == "" {
		return
	}
	r.writeOut(r.colorize("$ ", colorAccent) + cmdline + "\r\n")

	r.pauseInputMu.Lock()
	defer r.pauseInputMu.Unlock()

	// 暂时离开 raw 模式
	_ = r.Term.LeaveRaw()
	r.Editor.EnableBracketedPaste(false)
	defer func() {
		_ = r.Term.EnterRaw()
		r.Editor.EnableBracketedPaste(true)
	}()

	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", cmdline)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = r.WorkDir

	startedAt := time.Now()
	err := cmd.Run()
	elapsed := time.Since(startedAt).Truncate(time.Millisecond)

	if err == nil {
		r.writeOut(r.colorize(fmt.Sprintf("%sexit 0  %s\n", r.gl().result, elapsed), colorGreen))
	} else {
		exitCode := -1
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		}
		r.writeOut(r.colorize(fmt.Sprintf("%sexit %d  %s  (%v)\n", r.gl().result, exitCode, elapsed, err), colorRed))
	}
}

// AskPermission 工具调用前的权限弹窗（接入 Executor.SetAskPermission）
//
// 行为：
//   - PermissionMode == bypass         → 直接放行（与 src 对齐）
//   - PermissionMode == plan           → 直接拒绝（仅规划，不执行写操作）
//   - PermissionMode == acceptEdits + 编辑类工具 → 直接放行
//   - 其它情况 → 弹窗 [a]llow once / [s]tay / [d]eny
//
// 返回值：(allowed, error)；error 仅在 ctx 取消/IO 错误时
func (r *REPL) AskPermission(ctx context.Context, toolName string, input any, reason string) (bool, error) {
	switch r.PermissionMode {
	case "bypass":
		return true, nil
	case "plan":
		// 计划模式：拒绝所有写入；上层把"permission denied"作为 tool_result
		// 让模型继续推理而不真改文件
		return false, nil
	case "acceptEdits":
		if isEditTool(toolName) {
			return true, nil
		}
	}
	if r.PermissionDialog == nil {
		return true, nil
	}
	return r.PermissionDialog.Ask(ctx, r, toolName, input, reason)
}

// isEditTool 是否为编辑/写入类工具（用于 acceptEdits 模式自动放行）
//
// 与 src 对齐：FileWrite / FileEdit / NotebookEdit 等典型写工具。
// bash 类不在自动放行范围（潜在副作用太大）。
func isEditTool(name string) bool {
	switch name {
	case "file_edit", "file_write", "file_multi_edit",
		"notebook_edit", "edit", "write":
		return true
	}
	return false
}

// AskUser 在生成中需要向用户征询时调用：
// 暂停原始模式 + 暂停编辑器 → cooked 模式 prompt → 读一行 → 恢复 raw。
//
// 设计要点：
//   - 与 ReadLine 互斥（pauseInputMu），保证不会并发读 stdin
//   - 在 stderr 提示，避免污染对话 stdout
//   - ctx 取消时立即返回（防止生成期被 SIGINT 取消而 ask 卡住）
func (r *REPL) AskUser(ctx context.Context, question string) (string, error) {
	r.pauseInputMu.Lock()
	defer r.pauseInputMu.Unlock()

	var (
		answer  string
		readErr error
	)
	err := r.Term.WithCookedMode(func() error {
		// 临时禁用 bracketed paste，避免 cooked 模式下回显粘贴标记
		r.Editor.EnableBracketedPaste(false)
		defer r.Editor.EnableBracketedPaste(true)

		fmt.Fprintf(os.Stderr, "\r\n%s\r\n%s ",
			r.colorize("⏿ ask_user: "+question, colorYellow),
			r.colorize(">", colorAccent))

		type lineRes struct {
			line string
			err  error
		}
		ch := make(chan lineRes, 1)
		go func() {
			var b [1024]byte
			var sb strings.Builder
			for {
				n, err := os.Stdin.Read(b[:])
				if n > 0 {
					sb.Write(b[:n])
					if i := strings.IndexByte(sb.String(), '\n'); i >= 0 {
						line := sb.String()[:i]
						line = strings.TrimRight(line, "\r")
						ch <- lineRes{line: line}
						return
					}
				}
				if err != nil {
					ch <- lineRes{err: err}
					return
				}
			}
		}()
		select {
		case <-ctx.Done():
			readErr = ctx.Err()
			return readErr
		case res := <-ch:
			if res.err != nil && res.line == "" {
				readErr = res.err
				return readErr
			}
			answer = res.line
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if readErr != nil {
		return "", readErr
	}
	return answer, nil
}

// summarizeOneLine 把多行文本压成单行预览（与 truncOneLine 语义一致，保留别名以便阅读）
func summarizeOneLine(s string, max int) string {
	return truncOneLine(s, max)
}

// signalLoop 信号语义切换器
//
//	生成中收到 SIGINT（外部 kill -INT）→ cancel 当前查询
//	空闲态收到 SIGINT（外部 kill -INT）→ 设 wantsExit=true 触发优雅退出
//	注意：终端原始模式（MakeRaw）清除了 ISIG，故用户按下 Ctrl+C 产生的是字节
//	0x03，由 ReadLine 的 KeyCtrlC 路径处理（maybeOfferExit），而非此处。
//	此处的 SIGINT handler 主要应对外部信号（kill -INT、systemd、CI 等）。
//	SIGWINCH → 触发 Editor 重绘（粗略适配窗口尺寸变化）
func (r *REPL) signalLoop(parent context.Context, sigCh <-chan os.Signal, done <-chan struct{}) {
	for {
		select {
		case <-parent.Done():
			return
		case <-done:
			return
		case sig := <-sigCh:
			switch sig {
			case syscall.SIGINT:
				if r.generating.Load() {
					// 生成中：取消当前查询
					r.mu.Lock()
					cancel := r.activeCancel
					r.mu.Unlock()
					if cancel != nil {
						cancel()
					}
				} else {
					// 空闲态（外部发送的 SIGINT）：直接标记退出
					r.wantsExit.Store(true)
				}
			case syscall.SIGWINCH:
				// 编辑中（非生成）才重绘 prompt 区
				if !r.generating.Load() {
					r.Editor.PrintAboveLine("")
				}
			}
		}
	}
}

// maybeOfferExit 处理空闲态 Ctrl+C（KeyCtrlC 路径，非 SIGINT 信号路径）
//
// 行为：
//   - 第一次：提示用户"再按一次 Ctrl+C 退出"，记录时间
//   - 第二次（在 idleInterruptWindow 内）：设置 wantsExit=true，
//     主循环在下一次收到 ErrInterrupted 时执行优雅退出
//   - 若两次之间超过 idleInterruptWindow，重置计时器
func (r *REPL) maybeOfferExit() {
	now := time.Now()
	if !r.lastIdleInterrupt.IsZero() && now.Sub(r.lastIdleInterrupt) <= idleInterruptWindow {
		// 两次连按：标记退出
		r.wantsExit.Store(true)
		r.lastIdleInterrupt = time.Time{}
	} else {
		r.lastIdleInterrupt = now
		r.writeOut(r.colorize("（再按 Ctrl+C 退出，或输入 /exit）\r\n", colorDim))
	}
}

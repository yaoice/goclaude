package shell

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// handleWorkflowCmd 处理 /workflow [subcommand]。
//
// 子命令：
//   - (无参数)  → 等同 list
//   - list      → 列出所有可用 workflow
//   - plan <描述> → 通过 Plan Agent 生成 workflow 定义（不执行）
//   - run <name/描述> → 执行 workflow（无文件时自动通过 Plan Agent 生成）
//   - status [name] → 查看运行中 workflow 的节点状态
//   - cancel <name> → 取消运行中的 workflow
func (r *REPL) handleWorkflowCmd(args []string) {
	if r.Workflows == nil {
		r.writeOut(r.colorize("（workflow 服务未启用）\r\n", colorYellow))
		return
	}

	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}

	switch sub {
	case "list":
		r.renderWorkflowList()
	case "plan":
		if len(args) < 2 {
			r.writeOut(r.colorize("用法: /workflow plan \"<自然语言描述>\"\r\n", colorYellow))
			r.writeOut(r.colorize("  示例: /workflow plan \"构建用户认证模块\"\r\n", colorDim))
			return
		}
		r.renderWorkflowPlan(strings.Join(args[1:], " "))
	case "run":
		if len(args) < 2 {
			r.writeOut(r.colorize("用法: /workflow run <name 或 \"自然语言描述\">\r\n", colorYellow))
			r.writeOut(r.colorize("  有预定义文件: /workflow run code-review\r\n", colorDim))
			r.writeOut(r.colorize("  无文件时自动生成: /workflow run \"构建 REST API\"\r\n", colorDim))
			return
		}
		r.renderWorkflowRun(args[1])
	case "status":
		name := ""
		if len(args) > 1 {
			name = args[1]
		}
		r.renderWorkflowStatus(name)
	case "cancel":
		if len(args) < 2 {
			r.writeOut(r.colorize("用法: /workflow cancel <name>\r\n", colorYellow))
			return
		}
		r.renderWorkflowCancel(args[1])
	default:
		r.writeOut(r.colorize(
			fmt.Sprintf("未知子命令: %s。可用: list, plan, run, status, cancel\r\n", sub),
			colorYellow,
		))
	}
}

// renderWorkflowList 列出所有可用 workflow。
func (r *REPL) renderWorkflowList() {
	workflows := r.Workflows.List()
	if len(workflows) == 0 {
		r.writeOut(r.colorize("（暂无 workflow 定义）\r\n", colorDim))
		r.writeOut(r.colorize(
			"  将 .yaml 文件放入 ~/.goclaude/workflows/ 或 <project>/.goclaude/workflows/\r\n",
			colorDim,
		))
		return
	}

	for _, wf := range workflows {
		info := fmt.Sprintf("%d nodes", wf.NodeCount)
		r.writeOut("  " + r.colorize(wf.Name, colorCyan) + "  " +
			r.colorize(info, colorDim) + "\r\n")
		if wf.Description != "" {
			r.writeOut("    " + r.colorize(truncOneLine(wf.Description, 80), colorDim) + "\r\n")
		}
	}
}

// renderWorkflowRun 执行 workflow。
// 无预定义文件时自动通过 Plan Agent 生成（RunOrGenerate）。
func (r *REPL) renderWorkflowRun(name string) {
	// 先尝试查找预定义文件
	info, exists := r.Workflows.Get(name)

	if exists {
		r.writeOut(r.colorize(fmt.Sprintf("执行预定义 workflow: %s (%d nodes)\r\n", info.Name, info.NodeCount), colorCyan))
		if info.Description != "" {
			r.writeOut(r.colorize(info.Description+"\r\n", colorDim))
		}
	} else {
		r.writeOut(r.colorize(fmt.Sprintf("🔧 Plan Agent 分析中: %s\r\n", name), colorCyan))
		r.writeOut(r.colorize("  (无预定义文件，AI 自动生成 workflow 定义)\r\n", colorDim))
	}
	r.writeOut("\r\n")

	startTime := time.Now()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 进度显示 goroutine
	// 通过 channel 接收 RunOrGenerate 返回的真实 workflow name
	done := make(chan struct{})
	wfNameCh := make(chan string, 1)
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		wfName := name // 初始用用户输入名称
		for {
			select {
			case <-done:
				return
			case n := <-wfNameCh:
				// 收到真实 workflow name（RunOrGenerate 返回）
				wfName = n
			case <-ticker.C:
				if sv, err := r.Workflows.Status(wfName); err == nil && sv != nil {
					r.renderInlineStatus(sv)
				}
			}
		}
	}()

	// RunOrGenerate: 有预定义文件则直接执行，否则 Plan Agent → 生成 → 执行
	result, generated, err := r.Workflows.RunOrGenerate(ctx, name)
	// 通知进度 goroutine 真实的 workflow name
	if result != nil {
		select {
		case wfNameCh <- result.WorkflowName:
		default:
		}
	}
	close(done)
	// 清除内联状态行，恢复干净终端
	r.clearInlineStatus()
	r.writeOut("\r\n")

	if err != nil {
		r.writeOut(r.colorize(fmt.Sprintf("执行失败: %v\r\n", err), colorRed))
		return
	}

	elapsed := time.Since(startTime)
	statusColor := colorGreen
	if result.Failed > 0 {
		statusColor = colorRed
	} else if result.Skipped > 0 {
		statusColor = colorYellow
	}

	if generated {
		r.writeOut(r.colorize(fmt.Sprintf(
			"workflow %s %s (AI 生成) (%s)\r\n",
			result.WorkflowName, result.Status, elapsed.Round(time.Millisecond).String(),
		), statusColor))
	} else {
		r.writeOut(r.colorize(fmt.Sprintf(
			"workflow %s %s (%s)\r\n",
			result.WorkflowName, result.Status, elapsed.Round(time.Millisecond).String(),
		), statusColor))
	}
	r.writeOut(r.colorize(fmt.Sprintf(
		"  %d/%d completed  %d failed  %d skipped\r\n",
		result.Completed, result.TotalNodes, result.Failed, result.Skipped,
	), colorDim))

	if result.Output != "" {
		r.writeOut("\r\n" + strings.TrimRight(result.Output, "\n") + "\r\n")
	}
}

// renderWorkflowPlan 调用 Plan Agent 生成 workflow 定义，展示结果但不执行。
func (r *REPL) renderWorkflowPlan(description string) {
	r.writeOut(r.colorize(fmt.Sprintf("🔧 Plan Agent 分析中: %s\r\n", description), colorCyan))
	r.writeOut(r.colorize("  (AI 分析依赖关系、并行波次、推荐 subagent 类型)\r\n", colorDim))
	r.writeOut("\r\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gen, err := r.Workflows.Plan(ctx, description)
	if err != nil {
		r.writeOut(r.colorize(fmt.Sprintf("Plan Agent 失败: %v\r\n", err), colorRed))
		if gen != nil && gen.RawJSON != "" {
			r.writeOut("\r\n--- Raw Plan Agent Output ---\r\n")
			r.writeOut(r.colorize(truncOneLine(gen.RawJSON, 500)+"\r\n", colorDim))
		}
		return
	}

	r.writeOut(r.colorize(fmt.Sprintf("✅ Workflow 定义已生成: %s\r\n", gen.Name), colorGreen))
	r.writeOut(r.colorize(fmt.Sprintf("   描述: %s\r\n", gen.Description), colorDim))
	r.writeOut(r.colorize(fmt.Sprintf("   节点数: %d\r\n", gen.NodeCount), colorDim))
	if gen.SavedPath != "" {
		r.writeOut(r.colorize(fmt.Sprintf("   已保存: %s\r\n", gen.SavedPath), colorDim))
	}
	r.writeOut("\r\n")
	r.writeOut(r.colorize("执行: /workflow run "+gen.Name+"\r\n", colorCyan))
	r.writeOut("\r\n")

	// 展示生成的节点概要
	if gen.Workflow != nil {
		r.writeOut(r.colorize("节点概要:\r\n", colorDim))
	}
}

// renderWorkflowStatus 显示运行中 workflow 的状态。
func (r *REPL) renderWorkflowStatus(name string) {
	sv, err := r.Workflows.Status(name)
	if err != nil {
		r.writeOut(r.colorize(fmt.Sprintf("获取状态失败: %v\r\n", err), colorRed))
		return
	}
	if sv == nil {
		r.writeOut(r.colorize("暂无运行中的 workflow\r\n", colorDim))
		return
	}

	r.renderInlineStatus(sv)
}

// renderWorkflowCancel 取消运行中的 workflow。
func (r *REPL) renderWorkflowCancel(name string) {
	if err := r.Workflows.Cancel(name); err != nil {
		r.writeOut(r.colorize(fmt.Sprintf("取消失败: %v\r\n", err), colorRed))
		return
	}
	r.writeOut(r.colorize(fmt.Sprintf("已取消 workflow: %s\r\n", name), colorYellow))
}

// renderInlineStatus 渲染 workflow 节点状态（内联/刷新模式）。
//
// 使用 ANSI 光标控制实现原位刷新：
//   - 首次渲染：直接输出所有行
//   - 后续渲染：上移 lastStatusLines 行 → 逐行覆盖 → 多余旧行做清空
//
// 这样无论节点数量如何变化，终端只保留一组状态行，不会产生重复或残留行。
func (r *REPL) renderInlineStatus(sv *WorkflowStatusView) {
	r.statusLineMu.Lock()
	defer r.statusLineMu.Unlock()

	var sb strings.Builder

	// 构建新的状态内容
	statusIcon := "⏳"
	switch sv.Status {
	case "completed":
		statusIcon = "✅"
	case "failed":
		statusIcon = "❌"
	case "canceled":
		statusIcon = "⛔"
	}

	// 计算进度百分比
	completed := 0
	total := len(sv.Nodes)
	for _, node := range sv.Nodes {
		if node.Status == "completed" || node.Status == "failed" || node.Status == "skipped" || node.Status == "canceled" {
			completed++
		}
	}

	// 单行标题：图标 + workflow名 + 波次 + 进度条
	sb.WriteString(r.colorize(fmt.Sprintf(
		"%s workflow: %s  [wave %d/%d]  ",
		statusIcon, sv.Name, sv.CurrentWave+1, sv.TotalWaves,
	), colorCyan))

	// 简易 ASCII 进度条 [=====>    ] 50%
	barWidth := 20
	if sv.Progress > 0 {
		filled := int(sv.Progress / 100 * float64(barWidth))
		if filled > barWidth {
			filled = barWidth
		}
		sb.WriteString("[")
		sb.WriteString(strings.Repeat("=", filled))
		if filled < barWidth {
			sb.WriteString(">")
			sb.WriteString(strings.Repeat(" ", barWidth-filled-1))
		}
		sb.WriteString("]")
	} else {
		sb.WriteString("[")
		sb.WriteString(strings.Repeat(" ", barWidth))
		sb.WriteString("]")
	}
	sb.WriteString(r.colorize(fmt.Sprintf("  %d/%d  %.0f%%", completed, total, sv.Progress), colorDim))

	// 节点列表（最多显示 6 个活跃节点，避免刷屏）
	sb.WriteString("\r\n")
	shown := 0
	maxShow := 6
	for _, node := range sv.Nodes {
		// 只显示尚未完成/失败的活跃节点
		if node.Status == "completed" || node.Status == "skipped" || node.Status == "canceled" {
			continue
		}
		if shown >= maxShow {
			sb.WriteString(r.colorize(fmt.Sprintf("  ... 还有 %d 个节点 ...", total-completed-maxShow), colorDim))
			sb.WriteString("\r\n")
			break
		}
		shown++

		icon := "○"
		lineColor := colorDim
		switch node.Status {
		case "running":
			icon = "◐"
			lineColor = colorCyan
		case "failed":
			icon = "✘"
			lineColor = colorRed
		}

		display := node.NodeID
		if node.Name != "" {
			display = node.Name
		}

		sb.WriteString(r.colorize(fmt.Sprintf("  %s %s", icon, display), lineColor))
		if node.Status == "failed" && node.Error != "" {
			sb.WriteString(r.colorize(fmt.Sprintf(" (%s)", truncOneLine(node.Error, 30)), colorRed))
		}
		sb.WriteString("\r\n")
	}

	content := sb.String()

	// 计算新内容有多少行
	newLines := strings.Count(content, "\r\n")
	if newLines == 0 {
		newLines = 1
	}

	// ─── 清除上一次渲染的行 ───
	if r.lastStatusLines > 0 {
		// 光标上移到上一次渲染块的起始行
		r.writeOut(fmt.Sprintf("\x1b[%dA", r.lastStatusLines))
		// 逐行清空旧内容
		for i := 0; i < r.lastStatusLines; i++ {
			r.writeOut("\x1b[2K") // 清除当前行
			if i < r.lastStatusLines-1 {
				r.writeOut("\x1b[B") // 下移一行（除最后一行）
			}
		}
		// 光标回到清除起点
		if r.lastStatusLines > 1 {
			r.writeOut(fmt.Sprintf("\x1b[%dA", r.lastStatusLines-1))
		}
	}

	// ─── 写入新内容（覆盖在原位置） ───
	r.writeOut(content)

	// 如果新行数比旧行数少，填补空白行
	for i := newLines; i < r.lastStatusLines; i++ {
		r.writeOut("\x1b[2K\r\n")
	}

	// 光标保持在渲染块末尾，不回到顶部
	// （下次渲染时会从此处上移，实现"原位刷新"效果）

	r.lastStatusLines = newLines
}

// clearInlineStatus 清除当前内联状态块的所有行。
// 在 workflow 执行结束后调用，把终端恢复到干净状态。
func (r *REPL) clearInlineStatus() {
	r.statusLineMu.Lock()
	defer r.statusLineMu.Unlock()

	if r.lastStatusLines == 0 {
		return
	}

	// 光标上移到状态块起始行
	r.writeOut(fmt.Sprintf("\x1b[%dA", r.lastStatusLines))
	// 逐行擦除
	for i := 0; i < r.lastStatusLines; i++ {
		r.writeOut("\x1b[2K")
		if i < r.lastStatusLines-1 {
			r.writeOut("\x1b[B")
		}
	}
	// 光标回到清除起点
	if r.lastStatusLines > 1 {
		r.writeOut(fmt.Sprintf("\x1b[%dA", r.lastStatusLines-1))
	}

	r.lastStatusLines = 0
}

// ShowWorkflowsDialog 启动全屏 workflow 管理对话框（预留扩展点）。
func (r *REPL) ShowWorkflowsDialog() {
	// 当前使用 readline 文本模式；未来可扩展为 Bubble Tea 全屏对话框
	r.renderWorkflowList()
}

package shell

// subagent_progress.go —— subagent 执行过程的"阶段化"建模。
//
// 设计动机：
//   原始日志对每一轮（turn）都打印一行 `turn 2 bash`，长任务下会刷出几十行
//   密集、低信息密度的文本。本文件把零散的工具调用收敛为少数几个**逻辑阶段**
//   （团队创建 / 任务分配 / 协调 / 代码生成 / 文件写入 / 探索），渲染层据此把
//   "每轮一行"换成"每阶段一行"，让用户一眼看清子代理走到了哪一步。

import "strings"

// ---- 逻辑阶段标签（ASCII，避免 CJK 在终端里列宽错位）----
const (
	phaseExploration  = "Exploration"
	phaseTeamSetup    = "Team Setup"
	phaseTaskAssign   = "Task Assignment"
	phaseCoordination = "Coordination"
	phaseCodeGen      = "Code Generation"
	phaseFileWrite    = "File Writing"
	phaseWorking      = "Working"
)

// 各阶段对应的工具名集合（统一小写比较）。
var (
	teamSetupTools = map[string]bool{
		"team_create": true, "teamcreate": true,
		"team_delete": true, "teamdelete": true,
		"parse_team_intent": true, "parseteamintent": true,
		"auto_setup_team": true, "autosetupteam": true,
	}
	taskAssignTools = map[string]bool{
		"send_message": true, "sendmessage": true,
		"assign_task": true, "assigntask": true,
		"team_tasks": true, "teamtasks": true,
		"update_task": true, "updatetask": true,
		"claim_task": true, "claimtask": true,
		"create_task": true, "createtask": true,
	}
	coordinationTools = map[string]bool{
		"read_inbox": true, "readinbox": true,
		"list_peers": true, "listpeers": true,
		"get_team_status": true, "getteamstatus": true, "team_status": true,
	}
	fileWriteTools = map[string]bool{
		"write": true, "write_file": true, "writefile": true,
		"file_write": true, "filewrite": true, "write_to_file": true,
		"create": true, "edit": true, "str_replace": true, "strreplace": true,
		"multiedit": true, "multi_edit": true, "replace_in_file": true,
		"apply_patch": true, "applypatch": true, "notebook_edit": true,
		"notebookedit": true, "delete_file": true, "deletefile": true,
	}
	codeGenTools = map[string]bool{
		"bash": true, "shell": true, "exec": true, "run": true,
		"execute_command": true, "executecommand": true,
		"python": true, "node": true, "bashoutput": true,
	}
	exploreTools = map[string]bool{
		"read": true, "read_file": true, "readfile": true,
		"glob": true, "grep": true, "search": true, "ls": true,
		"list_dir": true, "listdir": true, "codebase_search": true,
		"search_file": true, "search_content": true,
		"web_search": true, "websearch": true, "web_fetch": true, "webfetch": true,
	}
)

// noisyCoordinationTools 列出主流程里需要"折叠为精简摘要状态"的内部协调工具。
//
// 这些工具（解析意图 / 自动建团 / 拉取收件箱）通常返回一大段 JSON，且在一轮里
// 可能被反复调用做重试或纠错；逐条展开会淹没真正重要的输出。渲染层据此把它们
// 压成单行，隐藏中间步骤。
var noisyCoordinationTools = map[string]bool{
	"parse_team_intent": true, "parseteamintent": true,
	"auto_setup_team": true, "autosetupteam": true,
	"read_inbox": true, "readinbox": true,
}

// toolKey 归一化工具名（小写 + 去空白），用于查表。
func toolKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// isNoisyCoordinationTool 判断一个工具是否应在主流程里被折叠为单行摘要。
func isNoisyCoordinationTool(name string) bool {
	return noisyCoordinationTools[toolKey(name)]
}

// classifyToolPhase 把工具名映射到逻辑阶段；空名返回 ""，无法识别返回 phaseWorking。
func classifyToolPhase(name string) string {
	n := toolKey(name)
	if n == "" {
		return ""
	}
	switch {
	case teamSetupTools[n]:
		return phaseTeamSetup
	case taskAssignTools[n]:
		return phaseTaskAssign
	case coordinationTools[n]:
		return phaseCoordination
	case fileWriteTools[n]:
		return phaseFileWrite
	case codeGenTools[n]:
		return phaseCodeGen
	case exploreTools[n]:
		return phaseExploration
	default:
		return phaseWorking
	}
}

// ---- 进度追踪器 ----

// subagentTracker 跟踪单个 subagent 的执行进度，把 per-turn 事件聚合为逻辑阶段。
//
// 采用"阶段去重"语义：每个阶段在整个生命周期内只首次出现时报告一次，
// 同阶段的后续调用（含重试 / 纠错 / 阶段间往返）一律折叠，保持步骤列表干净。
type subagentTracker struct {
	agentType string
	model     string
	seen      map[string]bool // 已出现过的阶段
	order     []string        // 阶段首次进入顺序
	// lastToolDetail 最近一轮中工具调用的参数摘要（如 bash 命令、文件路径）；
	// 用于在首次进入某阶段时在步骤行末尾打印 drill-down 细节。
	lastToolDetail string
}

func newSubagentTracker(agentType, model string) *subagentTracker {
	return &subagentTracker{
		agentType: agentType,
		model:     model,
		seen:      make(map[string]bool),
	}
}

// observe 记录一次工具调用（取自 Progress 事件的 LastTool）。
//
// 返回：
//   - phase：本次归类到的阶段（空名工具返回 ""）
//   - isFirst：该阶段是否**首次出现**（用于决定要不要打印一行步骤）
func (t *subagentTracker) observe(tool string) (phase string, isFirst bool) {
	phase = classifyToolPhase(tool)
	if phase == "" {
		return "", false
	}
	if t.seen[phase] {
		return phase, false
	}
	t.seen[phase] = true
	t.order = append(t.order, phase)
	return phase, true
}

// hasPhases 是否记录到任何阶段。
func (t *subagentTracker) hasPhases() bool {
	return t != nil && len(t.order) > 0
}

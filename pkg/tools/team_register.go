// Package tools - team_register 提供 5 个 team 工具的统一注册入口。
package tools

import (
	"github.com/anthropics/goclaude/pkg/application"
	"github.com/anthropics/goclaude/pkg/domain/tool"
)

// TeamRuntime 是 team 工具组的运行时身份。
//
// 每个 goclaude 进程对应一个 (TeamName, AgentName)。空值表示当前会话未加入
// 任何 team —— 工具仍会注册（让模型能创建 team），但调用时会要求显式传 team_name。
type TeamRuntime struct {
	// TeamName 当前会话加入的 team；空 = 未加入
	TeamName string
	// AgentName 本会话在 team 中的身份（leader 用 "team-lead"）
	AgentName string
	// Session 共享会话追踪器（可为 nil）；注入到每个 team 工具，使 team_create /
	// auto_setup_team 能登记 leader 身份，供上层 REPL 每轮自动处理 leader inbox。
	Session *TeamSession
}

// RegisterTeamTools 把 27 个 team 工具注册到 registry。
//
// 调用顺序无关；如果 svc 为 nil 则跳过（不会 panic），上层日志告警即可。
// 使用 Unregister + Register 模式以便重复注册时覆盖（与 SkillTool/AgentTool 注入风格一致）。
//
// 工具清单（基础 7 + 进阶 5 + 任务 9 + 规划 6 = 共 27 个）：
//   - team_create / team_delete       leader 创建 / 清理 team
//   - send_message                    通用消息发送（含 10 种协议消息类型）
//   - list_peers                      列出成员（含 status / heartbeat）
//   - read_inbox                      拉取未读（支持 since 游标 / limit 截断）
//   - get_team_status                 获取团队整体状态摘要
//   - parse_team_intent              解析自然语言中的团队创建意图
//   - assign_task / report_task       消息式任务分配 / 结果汇报（兼容旧流程）
//   - wait_for_message                阻塞等待未读消息（替代轮询）
//   - set_status                      声明 idle/working/blocked/error/done
//   - heartbeat                       刷新心跳，证明成员存活
//   【共享任务列表 CRUD】
//   - create_task                     在团队任务列表中创建新任务
//   - update_task                     更新指定任务的状态 / 分配 / 描述
//   - list_tasks                     列出团队任务列表（可选按状态过滤）
//   - get_task                       查看单个任务详情
//   - claim_task                     成员自主认领指定 pending 任务
//   - claim_any_task                 成员自主认领任意 pending 任务（自动选择）
//   - delete_task                    从任务列表中删除指定任务
//   【自动化工具】
//   - auto_setup_team                 一键创建团队、添加成员、创建任务
//   - auto_assign_task                自动将 pending 任务分配给空闲成员
//   【Plan-then-Execute 工具】
//   - initiate_planning               启动 Planning Phase，广播目标给成员
//   - collect_proposal                成员提交任务提案
//   - approve_plan                    leader 审批计划，转入 Executing Phase
//   - reject_plan                     leader 驳回计划，反馈给成员
//   - start_execution                 向成员派发已审批任务
//   - initiate_replan                 任务失败后暂停执行，返回 Planning Phase
//   - get_plan                        获取当前执行计划及验证摘要
func RegisterTeamTools(reg *tool.Registry, svc *application.TeamService, rt TeamRuntime) {
	if reg == nil || svc == nil {
		return
	}
	
	// 基础 6 个工具
	baseTools := []tool.Tool{
		NewTeamCreateTool(svc, rt.TeamName, rt.AgentName),
		NewTeamDeleteTool(svc, rt.TeamName, rt.AgentName),
		NewSendMessageTool(svc, rt.TeamName, rt.AgentName),
		NewListPeersTool(svc, rt.TeamName, rt.AgentName),
		NewReadInboxTool(svc, rt.TeamName, rt.AgentName),
		NewGetTeamStatusTool(svc, rt.TeamName, rt.AgentName),
		NewParseTeamIntentTool(svc, rt.TeamName, rt.AgentName),
	}
	
	// 进阶 5 个工具
	advancedTools := []tool.Tool{
		NewAssignTaskTool(svc, rt.TeamName, rt.AgentName),
		NewReportTaskTool(svc, rt.TeamName, rt.AgentName),
		NewWaitForMessageTool(svc, rt.TeamName, rt.AgentName),
		NewSetMemberStatusTool(svc, rt.TeamName, rt.AgentName),
		NewHeartbeatTool(svc, rt.TeamName, rt.AgentName),
	}
	
	// 任务管理 9 个工具（共享任务列表 + 自动化）
	taskTools := []tool.Tool{
		NewCreateTaskTool(svc, rt.TeamName, rt.AgentName),
		NewUpdateTaskTool(svc, rt.TeamName, rt.AgentName),
		NewListTasksTool(svc, rt.TeamName, rt.AgentName),
		NewGetTaskTool(svc, rt.TeamName, rt.AgentName),
		NewClaimTaskTool(svc, rt.TeamName, rt.AgentName),
		NewClaimAnyTaskTool(svc, rt.TeamName, rt.AgentName),
		NewDeleteTaskTool(svc, rt.TeamName, rt.AgentName),
		NewAutoSetupTeamTool(svc, rt.TeamName, rt.AgentName),
		NewAutoAssignTaskTool(svc, rt.TeamName, rt.AgentName),
	}

	// 规划 6 个工具（Plan-then-Execute 架构）
	planTools := []tool.Tool{
		NewInitiatePlanningTool(svc, rt.TeamName, rt.AgentName),
		NewCollectProposalTool(svc, rt.TeamName, rt.AgentName),
		NewApprovePlanTool(svc, rt.TeamName, rt.AgentName),
		NewRejectPlanTool(svc, rt.TeamName, rt.AgentName),
		NewStartExecutionTool(svc, rt.TeamName, rt.AgentName),
		NewInitiateReplanTool(svc, rt.TeamName, rt.AgentName),
		NewGetPlanTool(svc, rt.TeamName, rt.AgentName),
	}

	// 注册所有工具
	allTools := append(append(baseTools, advancedTools...), taskTools...)
	allTools = append(allTools, planTools...)
	for _, t := range allTools {
		// 注入共享会话追踪器（若提供），让 team_create / auto_setup_team 能登记
		// leader 身份。工具均内嵌 teamToolBase，故都实现 sessionAttacher。
		if rt.Session != nil {
			if sa, ok := t.(sessionAttacher); ok {
				sa.attachSession(rt.Session)
			}
		}
		reg.Unregister(t.Name())
		_ = reg.Register(t)
	}
}

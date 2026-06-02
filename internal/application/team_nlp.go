// Package application 提供 team 相关自然语言解析能力。
//
// ParseTeamSetupIntent 可以从自然语言文本中提取：
//   - 团队名称
//   - 成员列表（名字 + 角色）
//   - 任务列表（标题 + 描述）
//
// 这是对 CodeBuddy 文档中"自然语言创建团队"的最小化实现。
package application

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/anthropics/goclaude/internal/domain/team"
)

// TeamSetupIntent 是 ParseTeamSetupIntent 的解析结果。
type TeamSetupIntent struct {
	TeamName string
	Members  map[string]string
	Tasks    []TeamTaskIntent
}

// TeamTaskIntent 是单个任务的解析结果。
type TeamTaskIntent struct {
	Title       string
	Description string
	AssignedTo  string
}

// ParseTeamSetupIntent 从自然语言文本中提取团队创建意图。
//
// 支持的中文触发词：
//   - "创建团队" / "建团队" / "新建 team"
//   - "添加成员" / "加入成员"
//   - "创建任务" / "分配任务"
//
// 返回 nil 表示未识别到团队创建意图。
func ParseTeamSetupIntent(text string) *TeamSetupIntent {
	lower := strings.ToLower(text)
	
	// 检查是否包含团队创建触发词
	triggerWords := []string{
		"创建团队", "建团队", "新建 team", "create team",
		"创建 team", "setup team", "建立团队",
		"create a team", "setup a team", "create new team",
	}
	triggered := false
	for _, w := range triggerWords {
		if strings.Contains(lower, w) {
			triggered = true
			break
		}
	}
	if !triggered {
		return nil
	}
	
	intent := &TeamSetupIntent{
		Members: make(map[string]string),
		Tasks:   make([]TeamTaskIntent, 0),
	}
	
	// 1. 提取团队名称
	intent.TeamName = extractTeamName(text)
	
	// 2. 提取成员
	intent.Members = extractMembers(text)
	
	// 3. 提取任务
	intent.Tasks = extractTasks(text)
	
	if intent.TeamName == "" && len(intent.Members) == 0 && len(intent.Tasks) == 0 {
		return nil
	}
	
	return intent
}

// extractTeamName 从文本中提取团队名称。
func extractTeamName(text string) string {
	// 匹配模式： "创建团队 XXX" / "团队名：xxx" / "team name: xxx" / "叫 xxx 的团队"
	// 注意：Go regexp 不支持 \uXXXX，使用 \p{Han} 匹配中文字符
	patterns := []*regexp.Regexp{
		// 优先匹配 "创建团队 XXX" / "建团队 XXX" / "新建团队 XXX"
		regexp.MustCompile(`(?:创建|建|新建)\s*团队\s+([a-zA-Z0-9_\-\p{Han}]+)`),
		// 匹配 "团队名：xxx" / "团队: xxx"
		regexp.MustCompile(`团队[名称]*[：:]\s*([a-zA-Z0-9_\-\p{Han}]+)`),
		// 匹配 "team name: xxx"
		regexp.MustCompile(`team\s+name\s*[:：]\s*([a-zA-Z0-9_\-\p{Han}]+)`),
		// 匹配 "叫 XXX 的团队"
		regexp.MustCompile(`叫\s*"?([a-zA-Z0-9_\-\p{Han}]+)"?\s*的团队`),
		// 匹配 "名字叫 XXX"
		regexp.MustCompile(`名\s*字\s*叫\s*"?([a-zA-Z0-9_\-\p{Han}]+)"?`),
	}
	
	for _, re := range patterns {
		if matches := re.FindStringSubmatch(text); len(matches) > 1 {
			return strings.TrimSpace(matches[1])
		}
	}
	
	// 默认团队名
	return "default-team"
}

// extractMembers 从文本中提取成员列表。
func extractMembers(text string) map[string]string {
	members := make(map[string]string)
	
	// 匹配模式： "成员：alice(researcher), bob(coder)" / "添加成员 alice 作为 researcher"
	// 注意：括号需要转义，因为 ( 在正则中是特殊字符
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`成员[：:]\s*([^\n]+)`),
		regexp.MustCompile(`添加成员\s+([a-zA-Z0-9_\p{Han}]+)\s*[\(（]([a-zA-Z0-9_\p{Han}]+)[\)）]`),
		regexp.MustCompile(`([a-zA-Z0-9_\p{Han}]+)\s*[\(（]([a-zA-Z0-9_\p{Han}]+)[\)）]`),
	}
	
	for _, re := range patterns {
		if matches := re.FindAllStringSubmatch(text, -1); len(matches) > 0 {
			for _, m := range matches {
				if len(m) >= 3 {
					name := strings.TrimSpace(m[1])
					role := strings.TrimSpace(m[2])
					if name != "" {
						members[name] = role
					}
				}
			}
			break
		}
	}
	
	// 如果没找到，尝试提取 "和 alice、bob 一起"
	if len(members) == 0 {
		re := regexp.MustCompile(`[和与、,，]\s*([a-zA-Z0-9_]+)`)
		if matches := re.FindAllStringSubmatch(text, -1); len(matches) > 0 {
			for i, m := range matches {
				name := strings.TrimSpace(m[1])
				if name != "" {
					members[name] = fmt.Sprintf("member-%d", i+1)
				}
			}
		}
	}
	
	return members
}

// extractTasks 从文本中提取任务列表。
func extractTasks(text string) []TeamTaskIntent {
	var tasks []TeamTaskIntent
	
	// 匹配模式： "任务：xxx" / "创建任务 xxx" / "分配任务 xxx"
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`任务[：:]\s*([^\n]+)`),
		regexp.MustCompile(`创建任务\s+[""]?([^""\n]+)[""]?`),
		regexp.MustCompile(`分配任务\s+[""]?([^""\n]+)[""]?`),
		regexp.MustCompile(`[0-9]+[\.、]\s*([^\n]+)`), // "1. xxx" / "1、xxx"
	}
	
	for _, re := range patterns {
		if matches := re.FindAllStringSubmatch(text, -1); len(matches) > 0 {
			for _, m := range matches {
				if len(m) >= 2 {
					title := strings.TrimSpace(m[1])
					if title != "" {
						tasks = append(tasks, TeamTaskIntent{
							Title:       title,
							Description: title,
						})
					}
				}
			}
			break
		}
	}
	
	return tasks
}

// ToAutoSetupTeamInput 将解析结果转换为 AutoSetupTeamInput。
func (i *TeamSetupIntent) ToAutoSetupTeamInput() AutoSetupTeamInput {
	// 将 TeamTaskIntent 转换为 team.SharedTask
	tasks := make([]team.SharedTask, 0, len(i.Tasks))
	for _, t := range i.Tasks {
		tasks = append(tasks, team.SharedTask{
			Title:       t.Title,
			Description: t.Description,
			Status:       team.SharedTaskPending,
			AssignedTo:  t.AssignedTo,
		})
	}
	
	input := AutoSetupTeamInput{
		TeamName: i.TeamName,
		LeadAgentID: "team-lead",
		Members:  i.Members,
		Tasks:    tasks,
	}
	
	return input
}

// ToToolInput 将解析结果转换为 tool.Input（用于调用 auto_setup_team 工具）。
func (i *TeamSetupIntent) ToToolInput() map[string]interface{} {
	out := map[string]interface{}{
		"team_name": i.TeamName,
		"from":      "team-lead",
	}
	
	if len(i.Members) > 0 {
		members := make(map[string]interface{})
		for name, role := range i.Members {
			members[name] = role
		}
		out["members"] = members
	}
	
	if len(i.Tasks) > 0 {
		tasks := make([]interface{}, 0, len(i.Tasks))
		for _, t := range i.Tasks {
			task := map[string]interface{}{
				"title":       t.Title,
				"description": t.Description,
			}
			if t.AssignedTo != "" {
				task["assigned_to"] = t.AssignedTo
			}
			tasks = append(tasks, task)
		}
		out["tasks"] = tasks
	}
	
	return out
}

// ToJSON 将解析结果序列化为 JSON（用于调试）。
func (i *TeamSetupIntent) ToJSON() string {
	b, err := json.MarshalIndent(i.ToToolInput(), "", "  ")
	if err != nil {
		return fmt.Sprintf("%+v", i)
	}
	return string(b)
}

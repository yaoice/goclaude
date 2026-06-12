package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/yaoice/goclaude/pkg/application"
	"github.com/yaoice/goclaude/pkg/domain/tool"
)

// TestNaturalLanguageTeamCreation 测试自然语言触发团队创建
// 验证：用户输入自然语言 → parse_team_intent 解析 → auto_setup_team 执行
func TestNaturalLanguageTeamCreation(t *testing.T) {
	// 1. 准备测试环境
	svc := application.NewTeamService()
	if svc == nil {
		t.Skip("TeamService not available")
	}

	// 2. 创建 parse_team_intent 工具
	parseTool := NewParseTeamIntentTool(svc, "", "test-leader")
	if !parseTool.IsEnabled() {
		t.Error("ParseTeamIntentTool should be enabled")
	}

	// 3. 测试中文触发词
	t.Run("Chinese trigger words", func(t *testing.T) {
		testCases := []struct {
			input    string
			expected bool // 是否应该检测到意图
		}{
			{"创建团队 Alpha Squad，成员有 alice(researcher) 和 bob(coder)", true},
			{"建团队 Beta Team，任务是实现登录", true},
			{"新建 team Gamma，添加成员 charlie", true},
			{"建立团队 Delta Squad", true},
			{"今天天气不错", false}, // 不应触发
		}

		for _, tc := range testCases {
			t.Run(tc.input, func(t *testing.T) {
				// 调用 parse_team_intent
				result, err := parseTool.Call(context.Background(), tool.Input{
					"text": tc.input,
				}, nil)

				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}

				if result == nil {
					t.Fatal("Result should not be nil")
				}

				// 解析结果
				var output map[string]interface{}
				if err := json.Unmarshal([]byte(result.Content), &output); err != nil {
					t.Errorf("Failed to parse result: %v", err)
				}

				success, _ := output["success"].(bool)
				if success != tc.expected {
					t.Errorf("Expected success=%v, got %v for input: %s", tc.expected, success, tc.input)
				}

				// 如果检测到意图，应该返回 tool_input
				if success {
					if _, ok := output["tool_input"]; !ok {
						t.Error("Should return tool_input when intent is detected")
					}
					if _, ok := output["next_action"]; !ok {
						t.Error("Should return next_action when intent is detected")
					}
				}
			})
		}
	})

	// 4. 测试英文触发词
	t.Run("English trigger words", func(t *testing.T) {
		testCases := []struct {
			input    string
			expected bool
		}{
			{"Create team Epsilon with members alice(researcher) and bob(coder)", true},
			{"Setup team Zeta for code review", true},
			{"create a team called Theta", true},
			{"Hello world", false},
		}

		for _, tc := range testCases {
			t.Run(tc.input, func(t *testing.T) {
				result, err := parseTool.Call(context.Background(), tool.Input{
					"text": tc.input,
				}, nil)

				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}

				var output map[string]interface{}
				if err := json.Unmarshal([]byte(result.Content), &output); err != nil {
					t.Errorf("Failed to parse result: %v", err)
				}

				success, _ := output["success"].(bool)
				if success != tc.expected {
					t.Errorf("Expected success=%v, got %v for input: %s", tc.expected, success, tc.input)
				}
			})
		}
	})

	// 5. 测试提取准确性
	t.Run("Extraction accuracy", func(t *testing.T) {
		input := "创建团队 TestTeam，成员有 alice(researcher) 和 bob(coder)，任务是实现登录功能"

		result, err := parseTool.Call(context.Background(), tool.Input{
			"text": input,
		}, nil)

		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}

		var output map[string]interface{}
		if err := json.Unmarshal([]byte(result.Content), &output); err != nil {
			t.Errorf("Failed to parse result: %v", err)
		}

		// 验证成功
		success, _ := output["success"].(bool)
		if !success {
			t.Error("Should detect team setup intent")
		}

		// 验证 tool_input 结构
		toolInput, ok := output["tool_input"].(map[string]interface{})
		if !ok {
			t.Error("tool_input should be a map")
		}

		// 验证团队名称
		if teamName, ok := toolInput["team_name"].(string); !ok || teamName == "" {
			t.Error("Should extract team_name")
		}

		// 验证成员
		if members, ok := toolInput["members"].(map[string]interface{}); ok {
			if len(members) == 0 {
				t.Error("Should extract at least one member")
			}
		}

		// 验证任务
		if tasks, ok := toolInput["tasks"].([]interface{}); ok {
			if len(tasks) == 0 {
				t.Error("Should extract at least one task")
			}
		}
	})
}

// TestAutoSetupTeamWithParsedInput 测试使用解析结果自动设置团队
func TestAutoSetupTeamWithParsedInput(t *testing.T) {
	svc := application.NewTeamService()
	if svc == nil {
		t.Skip("TeamService not available")
	}

	// 使用唯一团队名避免冲突
	teamName := fmt.Sprintf("AutoTestTeam_%d_%d", time.Now().UnixNano(), os.Getpid())
	t.Logf("Using team name: %s", teamName)

	// 0. 清理：如果团队已存在，先删除（强制删除）
	deleteTool := NewTeamDeleteTool(svc, "", "test-leader")
	deleteResult, deleteErr := deleteTool.Call(context.Background(), tool.Input{
		"team_name": teamName,
		"from":      "team-lead",
		"force":     true,
	}, nil)
	t.Logf("Debug: deleteResult=%v, deleteErr=%v", deleteResult, deleteErr)

	// 确保测试结束时清理
	t.Cleanup(func() {
		deleteTool := NewTeamDeleteTool(svc, "", "test-leader")
		deleteTool.Call(context.Background(), tool.Input{
			"team_name": teamName,
			"from":      "team-lead",
			"force":     true,
		}, nil)
		t.Logf("Cleaned up team: %s", teamName)
	})

	// 1. 先解析自然语言
	parseTool := NewParseTeamIntentTool(svc, "", "test-leader")
	input := fmt.Sprintf("创建团队 %s，成员有 alice(researcher)，任务是测试自动设置", teamName)

	result, err := parseTool.Call(context.Background(), tool.Input{
		"text": input,
	}, nil)

	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	var output map[string]interface{}
	if err := json.Unmarshal([]byte(result.Content), &output); err != nil {
		t.Fatalf("Failed to parse result: %v", err)
	}

	success, _ := output["success"].(bool)
	if !success {
		t.Fatal("Should detect team setup intent")
	}

	// 2. 使用解析结果调用 auto_setup_team
	toolInput, ok := output["tool_input"].(map[string]interface{})
	if !ok {
		t.Fatal("tool_input should be a map")
	}

	// 转换 tool.Input
	autoSetupInput := tool.Input{}
	for k, v := range toolInput {
		autoSetupInput[k] = v
	}

	// 确保有 from 字段
	if _, ok := autoSetupInput["from"]; !ok {
		autoSetupInput["from"] = "team-lead"
	}

	// 创建 auto_setup_team 工具
	autoSetupTool := NewAutoSetupTeamTool(svc, "", "test-leader")

	// 调用 auto_setup_team
	setupResult, err := autoSetupTool.Call(context.Background(), autoSetupInput, nil)
	if err != nil {
		t.Errorf("Auto setup failed: %v", err)
	}

	// 检查返回结果是否为空
	if setupResult == nil {
		t.Fatal("setupResult is nil")
	}

	// 验证结果（注意：tool.NewResult 返回的是 Content 字符串，可能是 JSON 也可能是错误消息）
	t.Logf("Debug: setupResult.IsError=%v, Content=%s", setupResult.IsError, setupResult.Content)

	if setupResult.IsError {
		t.Errorf("Auto setup should succeed, got error: %s", setupResult.Content)
	}

	// 尝试解析 JSON（如果成功，应该返回 JSON）
	var setupOutput map[string]interface{}
	if err := json.Unmarshal([]byte(setupResult.Content), &setupOutput); err == nil {
		// 是 JSON，检查 success 字段
		if success, ok := setupOutput["success"].(bool); ok && !success {
			t.Errorf("Auto setup returned success=false: %v", setupOutput)
		}
		t.Logf("✓ Team created successfully: %v", setupOutput)
	} else {
		// 不是 JSON，可能是成功消息
		t.Logf("✓ Team created (non-JSON response): %s", setupResult.Content)
	}
}

// TestToolDescriptionContainsGuidance 测试工具描述是否包含使用指导
func TestToolDescriptionContainsGuidance(t *testing.T) {
	svc := application.NewTeamService()

	// 测试 parse_team_intent 描述
	t.Run("parse_team_intent description", func(t *testing.T) {
		tool := NewParseTeamIntentTool(svc, "", "")
		desc := tool.Description()

		// 应该包含触发词示例
		expectedKeywords := []string{
			"创建团队",
			"create team",
			"WHEN TO USE",
			"auto_setup_team",
		}

		for _, keyword := range expectedKeywords {
			if !contains(desc, keyword) {
				t.Errorf("Description should contain %q, got: %s", keyword, desc)
			}
		}
	})

	// 测试 auto_setup_team 描述
	t.Run("auto_setup_team description", func(t *testing.T) {
		tool := NewAutoSetupTeamTool(svc, "", "")
		desc := tool.Description()

		// 应该包含使用说明
		expectedKeywords := []string{
			"parse_team_intent",
			"WHEN TO USE",
			"team_name",
			"members",
			"tasks",
		}

		for _, keyword := range expectedKeywords {
			if !contains(desc, keyword) {
				t.Errorf("Description should contain %q, got: %s", keyword, desc)
			}
		}
	})
}

// TestSystemPromptContainsTeamGuidance 测试 system prompt 是否包含 team 工具引导
func TestSystemPromptContainsTeamGuidance(t *testing.T) {
	// 这个测试需要访问 builtin.go 中的 prompt 变量
	// 由于 prompt 是局部变量，我们需要通过 agent.Definition 来获取

	// TODO: 如果需要，可以在 builtin.go 中导出 GetGeneralPurposePrompt() 函数
	// 然后在这里测试

	t.Skip("Requires exporting prompt from builtin.go")
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && (s[0:len(substr)] == substr || contains(s[1:], substr)))
}

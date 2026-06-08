package application

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/anthropics/goclaude/pkg/domain/workflow"
)

// ============================================================================
// Tests: Dependency Output Injection (修复DAG数据流注入后的验证)
// ============================================================================

// capturedNodeExecutor 继承 mockNodeExecutor，同时捕获每个节点收到的 prompt（含注入的依赖输出）。
type capturedNodeExecutor struct {
	mockNodeExecutor
	capturedPrompts map[string]string // nodeID → 执行时收到的最终 prompt
}

func newCapturedNodeExecutor(runFn func(string) (string, error)) *capturedNodeExecutor {
	return &capturedNodeExecutor{
		mockNodeExecutor: *newMockNodeExecutor(runFn),
		capturedPrompts:  make(map[string]string),
	}
}

func (e *capturedNodeExecutor) ExecuteNode(ctx context.Context, node *workflow.Node) (*workflow.NodeResult, error) {
	e.mu.Lock()
	e.capturedPrompts[node.ID] = node.Prompt
	e.callOrder = append(e.callOrder, node.ID)
	e.mu.Unlock()

	output, execErr := e.runFn(node.ID)
	if execErr != nil {
		return &workflow.NodeResult{
			NodeID: node.ID, Status: workflow.NodeStatusFailed,
			Error: execErr.Error(),
		}, nil
	}
	return &workflow.NodeResult{
		NodeID: node.ID, Status: workflow.NodeStatusCompleted,
		Output: output,
	}, nil
}

func (e *capturedNodeExecutor) getPrompt(nodeID string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.capturedPrompts[nodeID]
}

// ---------------------------------------------------------------------------
// Test 1: 基础依赖输出注入 — A→B，验证 B 的 prompt 包含 A 的输出
// ---------------------------------------------------------------------------
func TestDependencyOutputInjection_BasicChain(t *testing.T) {
	svc := NewWorkflowService(nil, nil, WorkflowDefaults{}, nil)

	wfDef := &workflow.Workflow{
		Name: "basic-chain",
		Nodes: []*workflow.Node{
			{
				ID: "plan-arch", Name: "规划架构",
				SubagentType: "Explore", Prompt: "设计游戏架构",
				DependsOn: []string{}, FailureStrategy: workflow.FailureStrategyFailFast,
			},
			{
				ID: "create-files", Name: "创建文件",
				SubagentType: "general-purpose", Prompt: "根据架构方案创建文件",
				DependsOn: []string{"plan-arch"}, FailureStrategy: workflow.FailureStrategyFailFast,
			},
		},
	}

	plan, err := svc.ParseAndValidate(wfDef)
	if err != nil {
		t.Fatalf("ParseAndValidate 失败: %v", err)
	}
	if len(plan.Waves) != 2 {
		t.Fatalf("期望 2 波次, 实际 %d", len(plan.Waves))
	}

	archOutput := "## 游戏架构设计\n模块: player, enemy, bullet, collision\n技术栈: Python+Pygame"

	exec := newCapturedNodeExecutor(func(nodeID string) (string, error) {
		switch nodeID {
		case "plan-arch":
			return archOutput, nil
		case "create-files":
			return "项目结构创建完成", nil
		}
		return "", fmt.Errorf("unknown node: %s", nodeID)
	})

	result, _, err := svc.Execute(context.Background(), plan, exec)
	if err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}
	if result.Completed != 2 {
		t.Fatalf("期望 2 节点完成, 实际 completed=%d failed=%d", result.Completed, result.Failed)
	}

	// 验证 B 的 prompt 包含 A 的输出
	promptB := exec.getPrompt("create-files")
	if promptB == "" {
		t.Fatal("create-files 节点的 prompt 为空")
	}
	if !strings.Contains(promptB, archOutput) {
		t.Errorf("节点 create-files 的 prompt 应包含 plan-arch 的输出，但未找到\n"+
			"实际 prompt: %s\n期望包含: %s", truncateOutput(promptB, 500), truncateOutput(archOutput, 200))
	}
	if !strings.Contains(promptB, "根据架构方案创建文件") {
		t.Errorf("节点 create-files 的 prompt 应保留原始内容\n实际: %s", truncateOutput(promptB, 500))
	}

	t.Logf("✅ 基础依赖输出注入 (A→B) 通过 — B 的 prompt 包含 A 的输出 (%d bytes)", len(promptB))
}

// ---------------------------------------------------------------------------
// Test 2: 多依赖注入 — C 依赖 A+B，验证 C 收到两者输出
// ---------------------------------------------------------------------------
func TestDependencyOutputInjection_MultipleDeps(t *testing.T) {
	svc := NewWorkflowService(nil, nil, WorkflowDefaults{}, nil)

	wfDef := &workflow.Workflow{
		Name: "multi-deps",
		Nodes: []*workflow.Node{
			{ID: "scan-a", Name: "扫描A", SubagentType: "Explore", Prompt: "扫描模块A", DependsOn: []string{}},
			{ID: "scan-b", Name: "扫描B", SubagentType: "Explore", Prompt: "扫描模块B", DependsOn: []string{}},
			{ID: "merge", Name: "合并", SubagentType: "Plan", Prompt: "合并分析结果",
				DependsOn: []string{"scan-a", "scan-b"}, FailureStrategy: workflow.FailureStrategyFailFast},
		},
	}

	plan, err := svc.ParseAndValidate(wfDef)
	if err != nil {
		t.Fatalf("ParseAndValidate 失败: %v", err)
	}

	outputA := "模块A扫描结果: 3个文件"
	outputB := "模块B扫描结果: 5个文件"

	exec := newCapturedNodeExecutor(func(nodeID string) (string, error) {
		switch nodeID {
		case "scan-a":
			return outputA, nil
		case "scan-b":
			return outputB, nil
		case "merge":
			return "合并完成", nil
		}
		return "", fmt.Errorf("unknown")
	})

	result, _, err := svc.Execute(context.Background(), plan, exec)
	if err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}
	if result.Completed != 3 {
		t.Fatalf("期望 3 节点完成, 实际 %d", result.Completed)
	}

	promptMerge := exec.getPrompt("merge")
	if !strings.Contains(promptMerge, outputA) {
		t.Errorf("merge prompt 应包含 scan-a 的输出")
	}
	if !strings.Contains(promptMerge, outputB) {
		t.Errorf("merge prompt 应包含 scan-b 的输出")
	}
	if !strings.Contains(promptMerge, "合并分析结果") {
		t.Errorf("merge prompt 应保留原始内容")
	}

	t.Logf("✅ 多依赖注入 (A+B→C) 通过 — merge 包含两个依赖的输出")
}

// ---------------------------------------------------------------------------
// Test 3: 无依赖节点不应有注入（Wave 0 节点 prompt 应保持原始）
// ---------------------------------------------------------------------------
func TestDependencyOutputInjection_NoDepsShouldBeClean(t *testing.T) {
	svc := NewWorkflowService(nil, nil, WorkflowDefaults{}, nil)

	wfDef := &workflow.Workflow{
		Name: "no-deps",
		Nodes: []*workflow.Node{
			{ID: "start", Name: "Start", SubagentType: "Explore", Prompt: "初始任务", DependsOn: []string{}},
		},
	}

	plan, err := svc.ParseAndValidate(wfDef)
	if err != nil {
		t.Fatalf("ParseAndValidate 失败: %v", err)
	}

	exec := newCapturedNodeExecutor(func(nodeID string) (string, error) {
		return "done", nil
	})

	result, _, err := svc.Execute(context.Background(), plan, exec)
	if err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}
	if result.Completed != 1 {
		t.Fatalf("期望 1 节点完成, 实际 %d", result.Completed)
	}

	prompt := exec.getPrompt("start")
	if prompt != "初始任务" {
		t.Errorf("Wave 0 无依赖节点的 prompt 应保持不变，实际: %s", prompt)
	}

	t.Logf("✅ 无依赖节点 prompt 保持原始内容")
}

// ---------------------------------------------------------------------------
// Test 4: 依赖输出截断 — 超长输出应被截断到合理长度
// ---------------------------------------------------------------------------
func TestDependencyOutputInjection_Truncation(t *testing.T) {
	svc := NewWorkflowService(nil, nil, WorkflowDefaults{}, nil)

	wfDef := &workflow.Workflow{
		Name: "truncation-test",
		Nodes: []*workflow.Node{
			{ID: "producer", Name: "Producer", SubagentType: "Explore", Prompt: "产出一份超长报告", DependsOn: []string{}},
			{ID: "consumer", Name: "Consumer", SubagentType: "Plan", Prompt: "使用报告",
				DependsOn: []string{"producer"}, FailureStrategy: workflow.FailureStrategyFailFast},
		},
	}

	plan, err := svc.ParseAndValidate(wfDef)
	if err != nil {
		t.Fatalf("ParseAndValidate 失败: %v", err)
	}

	// 10KB 的输出，应被截断
	longOutput := strings.Repeat("数据", 5000) // ~10KB (每个中文字符3字节)

	exec := newCapturedNodeExecutor(func(nodeID string) (string, error) {
		switch nodeID {
		case "producer":
			return longOutput, nil
		case "consumer":
			return "已处理", nil
		}
		return "", fmt.Errorf("unknown")
	})

	result, _, err := svc.Execute(context.Background(), plan, exec)
	if err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}
	if result.Completed != 2 {
		t.Fatalf("期望 2 节点完成, 实际 %d", result.Completed)
	}

	prompt := exec.getPrompt("consumer")
	// 注入的上下文不应超过 4000 字节（每个中文字符 3 字节，截断限制是 4000 字符/字节？实际上我们是按字节截断）
	if len(prompt) > 8000 { // 原始prompt + 截断后的输出 + 元信息
		t.Errorf("consumer prompt 过长 (%d bytes)，依赖输出应被截断", len(prompt))
	}
	if strings.Contains(prompt, "[... output truncated") {
		t.Logf("✅ 超长依赖输出正确截断")
	} else {
		t.Logf("⚠ 依赖输出未超过截断阈值 (%d bytes 原文)", len(longOutput))
	}

	t.Logf("✅ 截断测试通过 — prompt 长度=%d bytes, 原文=%d bytes", len(prompt), len(longOutput))
}

// ---------------------------------------------------------------------------
// Test 5: 完整 airplane-war-game 11 节点 DAG
// 模拟所有节点输出并验证下游节点收到上游产出
// ---------------------------------------------------------------------------
func TestDependencyOutputInjection_AirplaneWarFullDAG(t *testing.T) {
	svc := NewWorkflowService(nil, nil, WorkflowDefaults{}, nil)

	// 使用与 airplane-war-game.json 完全一致的 DAG 结构
	wfDef := &workflow.Workflow{
		Name: "airplane-war-game",
		Nodes: []*workflow.Node{
			{
				ID: "plan-game-architecture", Name: "规划游戏架构",
				SubagentType: "Plan", Prompt: "规划飞机大战游戏架构",
				DependsOn: []string{}, TimeoutSec: 120, FailureStrategy: workflow.FailureStrategyFailFast,
			},
			{
				ID: "create-project-structure", Name: "创建项目结构",
				SubagentType: "general-purpose", Prompt: "根据游戏架构方案，创建项目结构",
				DependsOn: []string{"plan-game-architecture"}, TimeoutSec: 60, FailureStrategy: workflow.FailureStrategyFailFast,
			},
			{
				ID: "implement-game-config", Name: "实现游戏配置",
				SubagentType: "general-purpose", Prompt: "编写 game/config.py 配置常量",
				DependsOn: []string{"create-project-structure"}, TimeoutSec: 60, FailureStrategy: workflow.FailureStrategyFailFast,
			},
			{
				ID: "implement-player-class", Name: "实现玩家飞机类",
				SubagentType: "general-purpose", Prompt: "编写 game/player.py",
				DependsOn: []string{"implement-game-config"}, TimeoutSec: 120, FailureStrategy: workflow.FailureStrategyFailFast,
			},
			{
				ID: "implement-bullet-class", Name: "实现子弹类",
				SubagentType: "general-purpose", Prompt: "编写 game/bullet.py",
				DependsOn: []string{"implement-game-config"}, TimeoutSec: 60, FailureStrategy: workflow.FailureStrategyFailFast,
			},
			{
				ID: "implement-enemy-class", Name: "实现敌机类",
				SubagentType: "general-purpose", Prompt: "编写 game/enemy.py",
				DependsOn: []string{"implement-game-config"}, TimeoutSec: 120, FailureStrategy: workflow.FailureStrategyFailFast,
			},
			{
				ID: "implement-collision-and-score", Name: "实现碰撞检测和计分",
				SubagentType: "general-purpose", Prompt: "编写 game/utils.py",
				DependsOn: []string{"implement-player-class", "implement-bullet-class", "implement-enemy-class"},
				TimeoutSec: 120, FailureStrategy: workflow.FailureStrategyFailFast,
			},
			{
				ID: "implement-main-game-loop", Name: "实现游戏主循环",
				SubagentType: "general-purpose", Prompt: "编写 main.py",
				DependsOn: []string{"implement-collision-and-score"}, TimeoutSec: 180, FailureStrategy: workflow.FailureStrategyFailFast,
			},
			{
				ID: "add-visual-enhancements", Name: "添加视觉效果增强",
				SubagentType: "general-purpose", Prompt: "增强视觉效果",
				DependsOn: []string{"implement-main-game-loop"}, TimeoutSec: 120, FailureStrategy: workflow.FailureStrategyContinue,
			},
			{
				ID: "add-sound-effects", Name: "添加音效",
				SubagentType: "general-purpose", Prompt: "添加音效",
				DependsOn: []string{"implement-main-game-loop"}, TimeoutSec: 120, FailureStrategy: workflow.FailureStrategyContinue,
			},
			{
				ID: "test-and-verify-game", Name: "测试并验证游戏",
				SubagentType: "general-purpose", Prompt: "测试游戏",
				DependsOn: []string{"add-visual-enhancements", "add-sound-effects"}, TimeoutSec: 300, FailureStrategy: workflow.FailureStrategyFailFast,
			},
		},
	}

	plan, err := svc.ParseAndValidate(wfDef)
	if err != nil {
		t.Fatalf("ParseAndValidate 失败: %v", err)
	}

	if plan.TotalNodes != 11 {
		t.Fatalf("期望 11 节点, 实际 %d", plan.TotalNodes)
	}
	// 验证波次结构: Wave0(1)→Wave1(1)→Wave2(1)→Wave3(3)→Wave4(1)→Wave5(1)→Wave6(2)→Wave7(1)
	if len(plan.Waves) != 8 {
		t.Fatalf("期望 8 波次, 实际 %d", len(plan.Waves))
	}

	// 模拟每个节点输出与其 ID 相关的内容（模拟真实 subagent 产出）
	exec := newCapturedNodeExecutor(func(nodeID string) (string, error) {
		return fmt.Sprintf("[OUTPUT-%s] 任务完成", nodeID), nil
	})

	result, _, err := svc.Execute(context.Background(), plan, exec)
	if err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}
	if result.Completed != 11 {
		t.Errorf("期望 11 节点完成, 实际 completed=%d failed=%d skipped=%d",
			result.Completed, result.Failed, result.Skipped)
	}
	if result.Failed != 0 {
		t.Errorf("期望 0 失败, 实际 %d", result.Failed)
	}

	// 验证依赖注入：下游节点的 prompt 应包含上游输出
	tests := []struct {
		nodeID       string
		mustContain  []string // 必须包含的上游输出
		mustNotExist []string // 不应该包含的输出
	}{
		{
			nodeID:      "create-project-structure",
			mustContain: []string{"[OUTPUT-plan-game-architecture]"},
		},
		{
			nodeID:      "implement-game-config",
			mustContain: []string{"[OUTPUT-create-project-structure]"},
		},
		{
			nodeID: "implement-collision-and-score",
			mustContain: []string{
				"[OUTPUT-implement-player-class]",
				"[OUTPUT-implement-bullet-class]",
				"[OUTPUT-implement-enemy-class]",
			},
		},
		{
			nodeID:      "implement-main-game-loop",
			mustContain: []string{"[OUTPUT-implement-collision-and-score]"},
		},
		{
			nodeID:      "test-and-verify-game",
			mustContain: []string{"[OUTPUT-add-visual-enhancements]", "[OUTPUT-add-sound-effects]"},
		},
		{
			nodeID:       "plan-game-architecture",
			mustNotExist: []string{"[OUTPUT-"}, // Wave0 节点无依赖，不应有任何注入
		},
		{
			nodeID: "implement-player-class",
			// 只应包含直接依赖 (implement-game-config) 的输出
			// 不应包含更深层链路上的输出
			mustContain:  []string{"[OUTPUT-implement-game-config]"},
			mustNotExist: []string{"[OUTPUT-create-project-structure]", "[OUTPUT-plan-game-architecture]"},
		},
	}

	for _, tt := range tests {
		prompt := exec.getPrompt(tt.nodeID)
		for _, substr := range tt.mustContain {
			if !strings.Contains(prompt, substr) {
				t.Errorf("节点 %s 的 prompt 应包含 %q 但未找到\nprompt=%s",
					tt.nodeID, substr, truncateOutput(prompt, 300))
			}
		}
		for _, substr := range tt.mustNotExist {
			if strings.Contains(prompt, substr) {
				t.Errorf("节点 %s 的 prompt 不应包含 %q 但存在\nprompt=%s",
					tt.nodeID, substr, truncateOutput(prompt, 300))
			}
		}
	}

	// 验证调用顺序：上游必须先于下游
	order := exec.getCallOrder()
	pos := make(map[string]int)
	for i, id := range order {
		pos[id] = i
	}
	// 验证关键依赖链的时序
	checkOrder := func(before, after string) {
		if pos[after] <= pos[before] {
			t.Errorf("时序错误: %s (pos=%d) 应在 %s (pos=%d) 之后执行, 实际顺序=%v",
				after, pos[after], before, pos[before], order)
		}
	}
	checkOrder("plan-game-architecture", "create-project-structure")
	checkOrder("create-project-structure", "implement-game-config")
	checkOrder("implement-game-config", "implement-player-class")
	checkOrder("implement-player-class", "implement-collision-and-score")
	checkOrder("implement-collision-and-score", "implement-main-game-loop")
	checkOrder("implement-main-game-loop", "add-visual-enhancements")
	checkOrder("implement-main-game-loop", "add-sound-effects")
	checkOrder("add-visual-enhancements", "test-and-verify-game")
	checkOrder("add-sound-effects", "test-and-verify-game")

	t.Logf("✅ 完整 11 节点 airplane-war-game DAG 通过")
	t.Logf("   波次数: %d, 完成: %d, 失败: %d, 调用顺序: %v",
		len(plan.Waves), result.Completed, result.Failed, order)
}

// ---------------------------------------------------------------------------
// Test 6: 加载真实的 airplane-war-game.json 文件并执行
// ---------------------------------------------------------------------------
func TestDependencyOutputInjection_RealJSONFile(t *testing.T) {
	// 加载项目中已有的 airplane-war-game.json
	projectDir := "../../.goclaude/workflows"

	// 直接加载 JSON 文件
	data, err := os.ReadFile(projectDir + "/airplane-war-game.json")
	if err != nil {
		t.Skipf("跳过: 找不到测试用的 airplane-war-game.json (%v)", err)
		return
	}

	var wfDef workflow.Workflow
	if err := json.Unmarshal(data, &wfDef); err != nil {
		t.Fatalf("解析 airplane-war-game.json 失败: %v", err)
	}

	if err := wfDef.Validate(); err != nil {
		t.Fatalf("airplane-war-game.json 验证失败: %v", err)
	}
	t.Logf("✅ JSON 文件验证通过: %s (%d 节点)", wfDef.Name, len(wfDef.Nodes))

	svc := NewWorkflowService(nil, nil, WorkflowDefaults{}, nil)
	plan, err := svc.ParseAndValidate(&wfDef)
	if err != nil {
		t.Fatalf("拓扑排序失败: %v", err)
	}
	t.Logf("✅ 拓扑排序: %d 波次, 总节点=%d", len(plan.Waves), plan.TotalNodes)

	// 模拟执行所有节点
	exec := newCapturedNodeExecutor(func(nodeID string) (string, error) {
		return fmt.Sprintf("[REAL-OUTPUT-%s]", nodeID), nil
	})

	result, _, err := svc.Execute(context.Background(), plan, exec)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}

	if result.TotalNodes != len(wfDef.Nodes) {
		t.Errorf("节点数不匹配: result=%d def=%d", result.TotalNodes, len(wfDef.Nodes))
	}
	if result.Failed > 0 {
		t.Errorf("有 %d 个节点失败", result.Failed)
	}

	// 关键验证：node "implement-game-core" 依赖 "plan-game-architecture"
	// 其 prompt 应包含上游节点输出
	promptCore := exec.getPrompt("implement-game-core")
	if !strings.Contains(promptCore, "[REAL-OUTPUT-plan-game-architecture]") {
		t.Errorf("JSON 文件中的节点 implement-game-core 应包含上游输出，但未找到")
	}

	// 验证 Wave0 节点无注入 — prompt 应保留原始内容（不被依赖输出污染）
	promptPlan := exec.getPrompt("plan-game-architecture")
	if !strings.Contains(promptPlan, "规划一个飞机大战") || strings.Contains(promptPlan, "[REAL-OUTPUT") {
		t.Errorf("Wave0 节点 prompt 不应包含依赖注入标记，实际: %s", truncateOutput(promptPlan, 200))
	}

	t.Logf("✅ 真实 JSON 文件加载+执行通过 — %d/%d 完成, status=%s",
		result.Completed, result.TotalNodes, result.Status)
}

// ---------------------------------------------------------------------------
// Test 7: fail_fast 策略 + 依赖注入 — 上游失败时下游正确跳过
// ---------------------------------------------------------------------------
func TestDependencyOutputInjection_FailFastWithInjection(t *testing.T) {
	svc := NewWorkflowService(nil, nil, WorkflowDefaults{}, nil)

	wfDef := &workflow.Workflow{
		Name: "fail-fast-deps",
		Nodes: []*workflow.Node{
			{ID: "A", Name: "A", SubagentType: "Explore", Prompt: "A", DependsOn: []string{}},
			{ID: "B", Name: "B", SubagentType: "Explore", Prompt: "B needs A", DependsOn: []string{"A"},
				FailureStrategy: workflow.FailureStrategyFailFast},
		},
	}

	plan, err := svc.ParseAndValidate(wfDef)
	if err != nil {
		t.Fatalf("ParseAndValidate 失败: %v", err)
	}

	// A 失败 → B 应被跳过
	exec := newCapturedNodeExecutor(func(nodeID string) (string, error) {
		if nodeID == "A" {
			return "", fmt.Errorf("node A intentionally failed")
		}
		return "ok-" + nodeID, nil
	})

	result, _, err := svc.Execute(context.Background(), plan, exec)
	if err != nil {
		t.Fatalf("Execute 返回错误: %v", err)
	}
	if result.Failed != 1 {
		t.Errorf("期望 1 failed, 实际 %d", result.Failed)
	}
	if result.Skipped != 1 {
		t.Errorf("期望 1 skipped (B 因 A 失败被跳过), 实际 %d", result.Skipped)
	}
	if result.Status != workflow.WorkflowStatusFailed {
		t.Errorf("期望 status=failed, 实际 %s", result.Status)
	}

	t.Logf("✅ fail_fast + 依赖注入: A 失败 → B 跳过 (failed=%d skipped=%d)", result.Failed, result.Skipped)
}

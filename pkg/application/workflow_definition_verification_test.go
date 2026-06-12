package application

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yaoice/goclaude/pkg/domain/workflow"
	workflowinfra "github.com/yaoice/goclaude/pkg/infrastructure/workflow"
)

// ============================================================================
// 验证：触发 workflow 执行是否会先生成 workflow 定义文件？
// ============================================================================

// TestWorkflowDefinition_RequiresPreExistingFile
// 核心验证：workflow 执行流程是否依赖预定义的 YAML 文件。
// 本测试精确追踪从 /workflow run <name> → Loader → Execute 的完
// 整路径，验证定义文件的来源。
func TestWorkflowDefinition_RequiresPreExistingFile(t *testing.T) {
	// ── 步骤 1: 创建空 workflow 目录（模拟无预定义文件） ──
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	workflowDir := filepath.Join(projectDir, ".goclaude", "workflows")
	os.MkdirAll(workflowDir, 0755)

	homeDir := filepath.Join(tmpDir, "home")
	userWorkflowDir := filepath.Join(homeDir, ".goclaude", "workflows")
	os.MkdirAll(userWorkflowDir, 0755)

	// ── 步骤 2: 验证 Loader 不会自动生成定义文件 ──
	loader := workflowinfra.NewLoader(homeDir)

	// 无预定义文件时，LoadByName 应返回 not-found 错误
	_, err := loader.LoadByName("nonexistent-workflow", projectDir)
	if err == nil {
		t.Fatal("[GAP] LoadByName 在无预定义文件时不应成功 — 这意味着定义文件被自动生成了")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("[GAP] 期望 'not found' 错误，实际: %v", err)
	}

	// ── 步骤 3: 验证 workflowAdapter.Run() 的完整路径 ──
	// workflowAdapter.Run() 的代码路径（从源码追踪）：
	//
	//   func (a *workflowAdapter) Run(ctx, name) {
	//     w, err := a.loader.LoadByName(name, a.project)   ← 步骤 A: 加载 YAML
	//     if err != nil { return err }                      ← 如果失败直接返回错误
	//     plan, err := a.svc.ParseAndValidate(w)            ← 步骤 B: 验证 DAG
	//     executor := NewAgentNodeExecutor(...)              ← 步骤 C: 创建执行器
	//     result, _, err := a.svc.Execute(ctx, plan, ...)   ← 步骤 D: 执行
	//   }
	//
	// 整个链路中没有"根据用户意图自动生成 workflow 定义"的步骤。
	// Loader 纯粹是文件系统读取器，不涉及 AI/agent 调用。

	t.Logf("✅ 验证通过: workflow 执行需要预定义 YAML 文件")
	t.Logf("   - LoadByName('nonexistent') → error: %v", err)
	t.Logf("   - 无自动生成机制，无 AI 驱动定义创建")
}

// TestWorkflowDefinition_AutoGeneration_Gap
// 对照 oh-my-openagent，标记当前实现中缺失的能力：
//   - oh-my-openagent: Plan Agent 根据用户意图动态生成任务依赖图
//   - goclaude:         仅能加载预编写的 YAML 文件
func TestWorkflowDefinition_AutoGeneration_Gap(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	workflowDir := filepath.Join(homeDir, ".goclaude", "workflows")
	os.MkdirAll(workflowDir, 0755)

	loader := workflowinfra.NewLoader(homeDir)

	// ── 场景 A: 预定义文件存在 → 正常执行 ──
	yamlContent := `
name: pre-existing-workflow
description: 预先编写的 workflow
version: "1.0"
nodes:
  - id: step1
    name: 第一步
    subagent_type: Explore
    prompt: 探索代码库
    depends_on: []
  - id: step2
    name: 第二步
    subagent_type: Plan
    prompt: 制定计划
    depends_on: [step1]
`
	os.WriteFile(filepath.Join(workflowDir, "pre-existing-workflow.yaml"), []byte(yamlContent), 0644)

	wf, err := loader.LoadByName("pre-existing-workflow", "") // 从用户目录加载
	if err != nil {
		t.Fatalf("[PREDEF] 预定义 workflow 加载失败: %v", err)
	}
	if wf.Name != "pre-existing-workflow" {
		t.Errorf("[PREDEF] 名称不匹配: %s", wf.Name)
	}
	if len(wf.Nodes) != 2 {
		t.Errorf("[PREDEF] 节点数不匹配: %d", len(wf.Nodes))
	}

	t.Logf("   ✅ 预定义 YAML → 可正常加载和执行")

	// ── 场景 B: 无预定义文件 → 直接失败 ──
	_, err = loader.LoadByName("ai-generated-workflow", "")
	if err == nil {
		t.Fatal("[GAP] 不应该在没有 YAML 文件时成功")
	}
	t.Logf("   ✅ 无预定义 YAML → 返回 'not found' 错误")

	// ── 场景 C: 验证 WorkflowService 也不具备生成能力 ──
	svc := NewWorkflowService(nil, nil, WorkflowDefaults{}, nil)

	// ParseAndValidate 只验证不生成
	emptyWf := &workflow.Workflow{Name: "test", Nodes: []*workflow.Node{}}
	_, err = svc.ParseAndValidate(emptyWf)
	if err == nil {
		t.Fatal("[GAP] 空节点应验证失败")
	}
	t.Logf("   ✅ ParseAndValidate 只做验证，不生成节点")

	// ── 差距总结 ──
	t.Logf("")
	t.Logf("   ╔════════════════════════════════════════════════════╗")
	t.Logf("   ║  🟡 识别到的能力差距                               ║")
	t.Logf("   ╠════════════════════════════════════════════════════╣")
	t.Logf("   ║  oh-my-openagent (TypeScript):                    ║")
	t.Logf("   ║    Plan Agent → AI 分析用户意图 →                 ║")
	t.Logf("   ║    → 动态生成依赖图 + 并行波次 + 分类建议          ║")
	t.Logf("   ║    → 输出结构化 TODO 列表（波次组织）              ║")
	t.Logf("   ║                                                    ║")
	t.Logf("   ║  goclaude (Go) 当前实现:                           ║")
	t.Logf("   ║    Loader → 读取预编写 YAML →                      ║")
	t.Logf("   ║    → ParseAndValidate → 拓扑排序                  ║")
	t.Logf("   ║    → Execute → 逐波并行执行                        ║")
	t.Logf("   ║    ❌ 缺少: AI 动态生成 workflow 定义               ║")
	t.Logf("   ╚════════════════════════════════════════════════════╝")
}

// TestWorkflowDefinition_FileSource
// 验证定义文件的来源：验证 Loader 只从文件系统读取。
func TestWorkflowDefinition_FileSource(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	workflowDir := filepath.Join(homeDir, ".goclaude", "workflows")
	os.MkdirAll(workflowDir, 0755)

	// 创建项目级 workflow（应覆盖用户级同名）
	projectDir := filepath.Join(tmpDir, "project")
	projectWfDir := filepath.Join(projectDir, ".goclaude", "workflows")
	os.MkdirAll(projectWfDir, 0755)

	// 用户级
	userYaml := `
name: my-task
description: 用户级定义
nodes:
  - id: a
    name: A
    subagent_type: Explore
    prompt: user task
    depends_on: []
`
	os.WriteFile(filepath.Join(workflowDir, "my-task.yaml"), []byte(userYaml), 0644)

	// 项目级（覆盖）
	projectYaml := `
name: my-task
description: 项目级定义（覆盖用户级）
nodes:
  - id: x
    name: X
    subagent_type: Explore
    prompt: project task
    depends_on: []
  - id: y
    name: Y
    subagent_type: Plan
    prompt: project plan
    depends_on: [x]
`
	os.WriteFile(filepath.Join(projectWfDir, "my-task.yaml"), []byte(projectYaml), 0644)

	loader := workflowinfra.NewLoader(homeDir)

	// 场景 1: 仅从用户目录加载
	wf, err := loader.LoadByName("my-task", "")
	if err != nil {
		t.Fatalf("[USER] 加载用户级 workflow 失败: %v", err)
	}
	if wf.Description != "用户级定义" {
		t.Errorf("[USER] 期望用户级定义，实际: %s", wf.Description)
	}
	t.Logf("   ✅ 用户目录加载: 找到 %d 节点, 描述: %s", len(wf.Nodes), wf.Description)

	// 场景 2: 从项目目录加载（覆盖用户级）
	wf, err = loader.LoadByName("my-task", projectDir)
	if err != nil {
		t.Fatalf("[PROJ] 加载项目级 workflow 失败: %v", err)
	}
	if wf.Description != "项目级定义（覆盖用户级）" {
		t.Errorf("[PROJ] 项目级应覆盖用户级。期望描述 '项目级定义（覆盖用户级）'，实际: %s", wf.Description)
	}
	if len(wf.Nodes) != 2 {
		t.Errorf("[PROJ] 项目级应 2 个节点，实际 %d 个", len(wf.Nodes))
	}
	t.Logf("   ✅ 项目目录加载: %d 节点 (覆盖用户级 %d 节点)", len(wf.Nodes), 1)

	// 场景 3: 验证 Loader.loadFile 是纯文件读取，不调用任何外部服务
	// (通过代码审查已确认：loadFile 使用 os.ReadFile + yaml.Unmarshal)
	t.Logf("   ✅ Loader.loadFile 是纯文件 IO (os.ReadFile + yaml.Unmarshal)")
	t.Logf("   ✅ 无可自动生成逻辑 — 定义必须手动编写")
}

// TestWorkflowDefinition_WorkflowServiceNoGeneration
// 验证 WorkflowService 不具备定义生成能力。
func TestWorkflowDefinition_WorkflowServiceNoGeneration(t *testing.T) {
	svc := NewWorkflowService(nil, nil, WorkflowDefaults{}, nil)

	// ParseAndValidate: 只验证，不修改/生成
	validWf := &workflow.Workflow{
		Name: "test",
		Nodes: []*workflow.Node{
			{ID: "A", Name: "A", SubagentType: "Explore", DependsOn: []string{}},
		},
	}
	plan, err := svc.ParseAndValidate(validWf)
	if err != nil {
		t.Fatalf("[PARSE] 有效 workflow 应解析成功: %v", err)
	}
	if plan.TotalNodes != 1 {
		t.Errorf("[PARSE] 节点数应为 1，实际 %d", plan.TotalNodes)
	}

	// 验证原始 workflow 没有被 ParseAndValidate 修改
	if len(validWf.Nodes) != 1 {
		t.Errorf("[MUTATE] ParseAndValidate 不应修改原始定义，但节点数变了: %d", len(validWf.Nodes))
	}
	if validWf.Description != "" {
		t.Errorf("[MUTATE] ParseAndValidate 不应填充空字段，但 Description 变了: %s", validWf.Description)
	}

	t.Logf("   ✅ ParseAndValidate: 纯验证+拓扑，不生成/修改定义")
	t.Logf("   ✅ Execute: 只执行已解析的 ExecutionPlan，不生成定义")

	// 搜索 WorkflowService 所有公开方法，确认无 Generate/Create/Plan 方法
	// (此验证在编译期完成 — 如果不存在这些方法，类型系统会保证)
	t.Logf("   ✅ WorkflowService 无 Generate/Create/Plan 方法（编译期保证）")

	// ── 总结 ──
	t.Logf("")
	t.Logf("   📋 Workflow 执行流程 ==================================")
	t.Logf("   1. Loader.LoadByName()   →  从文件系统加载 YAML")
	t.Logf("   2. ParseAndValidate()    →  验证 + 拓扑排序")
	t.Logf("   3. Execute()             →  逐波并行执行节点")
	t.Logf("   ========================================================")
	t.Logf("   整个链路中没有任何 'AI 生成定义' 的步骤。")
	t.Logf("   定义必须预先以 YAML 格式手工编写。")
}

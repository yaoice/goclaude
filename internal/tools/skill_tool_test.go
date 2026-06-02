package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/anthropics/goclaude/internal/application"
	"github.com/anthropics/goclaude/internal/domain/skill"
	"github.com/anthropics/goclaude/internal/domain/tool"
)

// helperSkillService 构造一个仅含 bundled skill 的 SkillService（避开磁盘 IO）
func helperSkillService(t *testing.T, skills ...*skill.Skill) *application.SkillService {
	t.Helper()
	svc := application.NewSkillService(nil)
	for _, sk := range skills {
		svc.RegisterBundled(sk)
	}
	return svc
}

func TestSkillTool_Name_AliasesAndSchema(t *testing.T) {
	st := NewSkillTool(helperSkillService(t), "/tmp", "")

	if got := st.Name(); got != "Skill" {
		t.Errorf("Name() = %q, want Skill (与 src SKILL_TOOL_NAME 对齐)", got)
	}
	wantAliases := map[string]bool{"skill": true, "skill_load": true}
	for _, a := range st.Aliases() {
		if !wantAliases[a] {
			t.Errorf("unexpected alias %q", a)
		}
		delete(wantAliases, a)
	}
	if len(wantAliases) > 0 {
		t.Errorf("missing aliases: %v", wantAliases)
	}

	schema := st.InputSchema()
	props, _ := schema["properties"].(map[string]interface{})
	if _, ok := props["name"]; !ok {
		t.Error("schema missing `name`")
	}
	required, _ := schema["required"].([]string)
	if len(required) != 1 || required[0] != "name" {
		t.Errorf("required = %v, want [name]", required)
	}
}

func TestSkillTool_ValidateInput(t *testing.T) {
	st := NewSkillTool(helperSkillService(t), "/tmp", "")
	if err := st.ValidateInput(tool.Input{}); err == nil {
		t.Error("empty input should fail validation")
	}
	if err := st.ValidateInput(tool.Input{"name": "foo"}); err != nil {
		t.Errorf("valid input rejected: %v", err)
	}
}

func TestSkillTool_Call_NotFound_ReturnsErrorResultNotGoErr(t *testing.T) {
	svc := helperSkillService(t,
		&skill.Skill{Name: "alpha", Content: "alpha body"},
		&skill.Skill{Name: "beta", Content: "beta body"},
	)
	st := NewSkillTool(svc, "/tmp", "session-1")

	res, err := st.Call(context.Background(), tool.Input{"name": "nonexistent"}, nil)
	if err != nil {
		// 关键约定：找不到 skill 不应返回 Go err，而是 IsError 结果
		t.Fatalf("Call returned go error %v; expected IsError result", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected IsError result, got %+v", res)
	}
	// 错误信息应包含可用 skill 名以便模型纠偏
	if !strings.Contains(res.Content, "alpha") || !strings.Contains(res.Content, "beta") {
		t.Errorf("error result should hint available skills, got %q", res.Content)
	}
}

func TestSkillTool_Call_Found_ReturnsBody(t *testing.T) {
	svc := helperSkillService(t,
		&skill.Skill{Name: "commit", Content: "Write a clear commit message"},
	)
	st := NewSkillTool(svc, "/tmp", "session-1")

	res, err := st.Call(context.Background(), tool.Input{"name": "commit"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected IsError; got %+v", res)
	}
	if !strings.Contains(res.Content, "commit message") {
		t.Errorf("body not returned; got %q", res.Content)
	}
}

func TestSkillTool_Call_NoServiceInjected(t *testing.T) {
	// 防御：service=nil 时不应 panic，应返回 IsError
	st := &SkillTool{service: nil, cwd: "/tmp"}
	res, err := st.Call(context.Background(), tool.Input{"name": "x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || !res.IsError {
		t.Errorf("nil service should yield IsError, got %+v", res)
	}
}

func TestSkillTool_Call_ArgsPlaceholderSubstitution(t *testing.T) {
	// SkillService.RenderWith 会替换 ${ARGS}；通过工具透传该参数
	svc := helperSkillService(t,
		&skill.Skill{Name: "echo", Content: "args=${ARGS}"},
	)
	st := NewSkillTool(svc, "/tmp", "")

	res, err := st.Call(context.Background(), tool.Input{
		"name": "echo",
		"args": "hello world",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	if !strings.Contains(res.Content, "args=hello world") {
		t.Errorf("ARGS not substituted; got %q", res.Content)
	}
}

func TestSkillTool_Call_EmptyBody_ReturnsError(t *testing.T) {
	// 防御：skill 存在但 Content="" 时不应返回空字符串成功结果
	svc := helperSkillService(t,
		&skill.Skill{Name: "empty", Content: ""},
	)
	st := NewSkillTool(svc, "/tmp", "")
	res, err := st.Call(context.Background(), tool.Input{"name": "empty"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || !res.IsError {
		t.Errorf("empty body should yield IsError, got %+v", res)
	}
}

func TestSkillTool_CheckPermissions_AlwaysAllow(t *testing.T) {
	st := NewSkillTool(helperSkillService(t), "/tmp", "")
	for _, mode := range []tool.PermissionMode{
		tool.PermissionModeDefault,
		tool.PermissionModePlan,
		tool.PermissionModeAcceptEdits,
		tool.PermissionModeBypass,
	} {
		res, err := st.CheckPermissions(context.Background(), tool.Input{"name": "x"},
			&tool.PermissionContext{Mode: mode})
		if err != nil {
			t.Fatalf("mode %s: error %v", mode, err)
		}
		if res.Behavior != tool.PermissionAllow {
			t.Errorf("mode %s: behavior = %v, want Allow", mode, res.Behavior)
		}
	}
}

// ---------- AgentTool 改名验证 ----------

func TestAgentTool_NameAndAliases_AlignedWithSrc(t *testing.T) {
	at := NewAgentTool()
	if at.Name() != "Agent" {
		t.Errorf("Agent tool name = %q, want Agent (与 src AGENT_TOOL_NAME 对齐)", at.Name())
	}
	got := at.Aliases()
	wantSet := map[string]bool{"Task": false, "agent": false}
	for _, a := range got {
		if _, ok := wantSet[a]; ok {
			wantSet[a] = true
		} else {
			t.Errorf("unexpected alias %q", a)
		}
	}
	for k, seen := range wantSet {
		if !seen {
			t.Errorf("missing alias %q", k)
		}
	}
}

func TestAgentTool_RegistryAliasResolution(t *testing.T) {
	// 验证 registry 通过 alias 仍能找到 AgentTool（向后兼容）
	reg := tool.NewRegistry()
	at := NewAgentTool()
	if err := reg.Register(at); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Agent", "Task", "agent"} {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("registry.Get(%q) failed; alias resolution broken", name)
		}
	}
}

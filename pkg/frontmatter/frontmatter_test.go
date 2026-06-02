package frontmatter

import (
	"reflect"
	"testing"
)

func TestParse_Basic(t *testing.T) {
	content := `---
name: my-skill
description: A test skill
user-invocable: true
---
This is the body.
`
	data, body, err := Parse(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if GetString(data, "name") != "my-skill" {
		t.Errorf("name = %q", GetString(data, "name"))
	}
	if GetString(data, "description") != "A test skill" {
		t.Errorf("description = %q", GetString(data, "description"))
	}
	if !GetBool(data, "user-invocable") {
		t.Errorf("user-invocable should be true")
	}
	if body != "This is the body.\n" {
		t.Errorf("body = %q", body)
	}
}

func TestParse_NoFrontmatter(t *testing.T) {
	content := "# Just a header\n\nNo frontmatter here."
	data, body, err := Parse(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("expected empty data, got %v", data)
	}
	if body != content {
		t.Errorf("body should be original content")
	}
}

func TestParse_InlineArray(t *testing.T) {
	content := `---
allowed-tools: [Read, Write, Edit]
aliases: ["alpha", "beta"]
---
body
`
	data, _, err := Parse(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tools := GetStringSlice(data, "allowed-tools")
	if !reflect.DeepEqual(tools, []string{"Read", "Write", "Edit"}) {
		t.Errorf("allowed-tools = %v", tools)
	}
	aliases := GetStringSlice(data, "aliases")
	if !reflect.DeepEqual(aliases, []string{"alpha", "beta"}) {
		t.Errorf("aliases = %v", aliases)
	}
}

func TestParse_DashArray(t *testing.T) {
	content := `---
paths:
  - src/**/*.ts
  - "*.go"
---
body
`
	data, _, err := Parse(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	paths := GetStringSlice(data, "paths")
	if !reflect.DeepEqual(paths, []string{"src/**/*.ts", "*.go"}) {
		t.Errorf("paths = %v", paths)
	}
}

func TestParse_OptionalBool(t *testing.T) {
	content := `---
name: x
---
`
	data, _, _ := Parse(content)
	if GetBoolPtr(data, "user-invocable") != nil {
		t.Errorf("user-invocable should be nil when absent")
	}

	content2 := `---
user-invocable: false
---
`
	data2, _, _ := Parse(content2)
	p := GetBoolPtr(data2, "user-invocable")
	if p == nil || *p != false {
		t.Errorf("user-invocable should be *false, got %v", p)
	}
}

func TestParse_LiteralBlock_PreservesSiblingKeys(t *testing.T) {
	// C2 修复回归：多行 | 字面值不能吞掉后续 key
	content := `---
name: x
description: |
  multi
  line
  text
model: haiku
when_to_use: 直接调用
---
body
`
	data, _, err := Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	if got := GetString(data, "description"); got != "multi\nline\ntext" {
		t.Errorf("description = %q", got)
	}
	if got := GetString(data, "model"); got != "haiku" {
		t.Errorf("model = %q (literal block 吞掉了 sibling key)", got)
	}
	if got := GetString(data, "when_to_use"); got != "直接调用" {
		t.Errorf("when_to_use = %q", got)
	}
}

func TestParse_LiteralBlock_VariableIndent(t *testing.T) {
	// 4 空格缩进的 | 块
	content := `---
desc: |
    line1
    line2
next: ok
---
`
	data, _, _ := Parse(content)
	if got := GetString(data, "desc"); got != "line1\nline2" {
		t.Errorf("desc = %q", got)
	}
	if got := GetString(data, "next"); got != "ok" {
		t.Errorf("next = %q", got)
	}
}

func TestParse_LiteralBlock_BlankLinesPreserved(t *testing.T) {
	content := `---
notes: |
  para1

  para2
end: y
---
`
	data, _, _ := Parse(content)
	got := GetString(data, "notes")
	if got != "para1\n\npara2" {
		t.Errorf("notes = %q (空行应被保留)", got)
	}
	if GetString(data, "end") != "y" {
		t.Errorf("end key 丢失")
	}
}

// 嵌套对象当前不完整解析（没有 yaml.v3 依赖），但应该至少不崩溃，
// 且后续顶层 key 仍能被正确识别（避免 hooks 嵌套吞掉 sibling 字段）
func TestParse_NestedObject_DoesNotCrash(t *testing.T) {
	content := `---
name: x
hooks:
  PreToolUse:
    - matcher: Bash
description: keeps working
---
body
`
	data, _, err := Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	if GetString(data, "name") != "x" {
		t.Errorf("name 丢失: %v", data)
	}
	// 关键：description 必须能正确解析（嵌套对象不应吞掉它）
	if GetString(data, "description") != "keeps working" {
		t.Errorf("description 被嵌套块吞掉: data=%v", data)
	}
}

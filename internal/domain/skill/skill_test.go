package skill

import "testing"

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := NewRegistry()

	sk := &Skill{
		Name:        "verify",
		Description: "验证代码",
		Aliases:     []string{"check"},
		IsEnabled:   true,
	}
	reg.Register(sk)

	// 按名称查找
	found, ok := reg.Get("verify")
	if !ok {
		t.Fatal("skill not found by name")
	}
	if found.Name != "verify" {
		t.Errorf("expected verify, got %s", found.Name)
	}

	// 按别名查找
	found, ok = reg.Get("check")
	if !ok {
		t.Fatal("skill not found by alias")
	}
	if found.Name != "verify" {
		t.Error("alias should resolve to same skill")
	}

	// 不存在的
	if _, ok = reg.Get("nonexistent"); ok {
		t.Error("should not find nonexistent skill")
	}
}

func TestRegistry_All(t *testing.T) {
	reg := NewRegistry()

	reg.Register(&Skill{Name: "a", IsEnabled: true})
	reg.Register(&Skill{Name: "b", IsEnabled: true})
	reg.Register(&Skill{Name: "c", IsEnabled: false})

	all := reg.All()
	if len(all) != 3 {
		t.Errorf("expected 3, got %d", len(all))
	}

	enabled := reg.Enabled()
	if len(enabled) != 2 {
		t.Errorf("expected 2 enabled, got %d", len(enabled))
	}
}

func TestRegistry_NoDuplicatesInAll(t *testing.T) {
	reg := NewRegistry()

	// 带别名的技能不应在 All() 中重复出现（按 name 主键存储）
	reg.Register(&Skill{
		Name:      "debug",
		Aliases:   []string{"dbg", "troubleshoot"},
		IsEnabled: true,
	})

	all := reg.All()
	if len(all) != 1 {
		t.Errorf("expected 1 unique skill, got %d", len(all))
	}
}

func TestRegistry_Conditional(t *testing.T) {
	reg := NewRegistry()

	reg.RegisterConditional(&Skill{
		Name:      "react-helper",
		Paths:     []string{"src/**/*.tsx"},
		IsEnabled: true,
	})

	// 条件 skill 不应出现在主索引
	if _, ok := reg.Get("react-helper"); ok {
		t.Error("conditional skill should not be in main index")
	}
	if got := len(reg.Conditional()); got != 1 {
		t.Errorf("expected 1 conditional skill, got %d", got)
	}

	// 激活后应出现
	if !reg.Activate("react-helper") {
		t.Error("Activate should return true")
	}
	if _, ok := reg.Get("react-helper"); !ok {
		t.Error("activated skill should be in main index")
	}
	if got := len(reg.Conditional()); got != 0 {
		t.Errorf("expected 0 conditional after activation, got %d", got)
	}
}

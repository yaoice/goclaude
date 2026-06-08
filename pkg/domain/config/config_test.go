package config

import "testing"

func TestMerger_SimpleOverride(t *testing.T) {
	merger := NewMerger()

	// 低优先级
	merger.AddLayer(SettingsData{
		Source: SourceUser,
		Data: map[string]interface{}{
			"theme": "dark",
			"model": "claude-haiku",
		},
	})

	// 高优先级
	merger.AddLayer(SettingsData{
		Source: SourceProject,
		Data: map[string]interface{}{
			"model": "claude-sonnet",
		},
	})

	result := merger.Merge()

	// 高优先级覆盖
	if result["model"] != "claude-sonnet" {
		t.Errorf("expected claude-sonnet, got %v", result["model"])
	}
	// 低优先级保留
	if result["theme"] != "dark" {
		t.Errorf("expected dark, got %v", result["theme"])
	}
}

func TestMerger_ArrayConcat(t *testing.T) {
	merger := NewMerger()

	merger.AddLayer(SettingsData{
		Source: SourceUser,
		Data: map[string]interface{}{
			"allowedTools": []interface{}{"file_read", "glob"},
		},
	})

	merger.AddLayer(SettingsData{
		Source: SourceProject,
		Data: map[string]interface{}{
			"allowedTools": []interface{}{"bash", "file_read"},
		},
	})

	result := merger.Merge()

	tools, ok := result["allowedTools"].([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", result["allowedTools"])
	}

	// 应该concat + dedup
	expected := map[string]bool{"file_read": true, "glob": true, "bash": true}
	if len(tools) != len(expected) {
		t.Errorf("expected %d tools, got %d: %v", len(expected), len(tools), tools)
	}
	for _, tool := range tools {
		if !expected[tool] {
			t.Errorf("unexpected tool: %s", tool)
		}
	}
}

func TestMerger_DeepMerge(t *testing.T) {
	merger := NewMerger()

	merger.AddLayer(SettingsData{
		Source: SourceUser,
		Data: map[string]interface{}{
			"api": map[string]interface{}{
				"provider": "anthropic",
				"timeout":  30,
			},
		},
	})

	merger.AddLayer(SettingsData{
		Source: SourceProject,
		Data: map[string]interface{}{
			"api": map[string]interface{}{
				"timeout": 60,
				"model":   "claude-sonnet",
			},
		},
	})

	result := merger.Merge()

	api, ok := result["api"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", result["api"])
	}

	if api["provider"] != "anthropic" {
		t.Errorf("expected anthropic, got %v", api["provider"])
	}
	if api["timeout"] != 60 {
		t.Errorf("expected 60, got %v", api["timeout"])
	}
	if api["model"] != "claude-sonnet" {
		t.Errorf("expected claude-sonnet, got %v", api["model"])
	}
}

func TestMerger_EmptyLayers(t *testing.T) {
	merger := NewMerger()
	result := merger.Merge()

	if len(result) != 0 {
		t.Errorf("expected empty result, got %v", result)
	}
}

func TestMerger_FiveLayerPriority(t *testing.T) {
	merger := NewMerger()

	// 按优先级从低到高添加
	layers := []SettingsSource{SourcePlugin, SourceUser, SourceProject, SourceLocal, SourcePolicy}
	for i, source := range layers {
		merger.AddLayer(SettingsData{
			Source: source,
			Data: map[string]interface{}{
				"priority_test": i,
			},
		})
	}

	result := merger.Merge()

	// 最高优先级(policy=4)应该获胜
	if result["priority_test"] != 4 {
		t.Errorf("expected 4 (policy), got %v", result["priority_test"])
	}
}

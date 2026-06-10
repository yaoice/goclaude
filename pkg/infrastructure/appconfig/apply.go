package appconfig

import (
	"fmt"
	"strconv"
	"time"
)

// applyMap 把 yamlite 解析得到的嵌套 map 应用到 cfg
//
// 仅覆盖 map 中存在的字段，缺失字段保留 cfg 既有值（实现"部分覆盖"语义）。
func applyMap(cfg *Config, m map[string]interface{}) {
	if api, ok := getMap(m, "api"); ok {
		applyAPI(&cfg.API, api)
	}
	if provs, ok := getMap(m, "providers"); ok {
		if cfg.Providers == nil {
			cfg.Providers = map[string]ProviderConfig{}
		}
		for name, raw := range provs {
			pm, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			pc := cfg.Providers[name]
			applyProvider(&pc, pm)
			cfg.Providers[name] = pc
		}
	}
	if engine, ok := getMap(m, "engine"); ok {
		applyEngine(&cfg.Engine, engine)
	}
	if tools, ok := getMap(m, "tools"); ok {
		applyTools(&cfg.Tools, tools)
	}
	if mcp, ok := getMap(m, "mcp"); ok {
		applyMCP(&cfg.MCP, mcp)
	}
	if at, ok := getMap(m, "agent_teams"); ok {
		applyAgentTeams(&cfg.AgentTeams, at)
	}
	if perm, ok := getMap(m, "permissions"); ok {
		applyPermissions(&cfg.Permissions, perm)
	}
	if sb, ok := getMap(m, "sandbox"); ok {
		applySandbox(&cfg.Sandbox, sb)
	}
	if sess, ok := getMap(m, "session"); ok {
		applySession(&cfg.Session, sess)
	}
	if tui, ok := getMap(m, "tui"); ok {
		applyTUI(&cfg.TUI, tui)
	}
	if sp, ok := getMap(m, "system_prompt"); ok {
		applySystemPrompt(&cfg.SystemPrompt, sp)
	}
	if ws, ok := getMap(m, "workspace"); ok {
		applyWorkspace(&cfg.Workspace, ws)
	}
	if ltm, ok := getMap(m, "longterm_memory"); ok {
		applyLongTermMemory(&cfg.LongTermMemory, ltm)
	}
}

func applyAPI(c *APIConfig, m map[string]interface{}) {
	if v, ok := getString(m, "provider"); ok {
		c.Provider = v
	}
	if v, ok := getString(m, "model"); ok {
		c.Model = v
	}
	if v, ok := getInt(m, "max_tokens"); ok {
		c.MaxTokens = v
	}
	if v, ok := getFloat(m, "temperature"); ok {
		c.Temperature = v
	}
	if v, ok := getFloat(m, "top_p"); ok {
		c.TopP = v
	}
	if v, ok := getBool(m, "stream"); ok {
		c.Stream = v
	}
}

func applyProvider(c *ProviderConfig, m map[string]interface{}) {
	if v, ok := getString(m, "base_url"); ok {
		c.BaseURL = v
	}
	if v, ok := getString(m, "api_version"); ok {
		c.APIVersion = v
	}
	if v, ok := getString(m, "default_model"); ok {
		c.DefaultModel = v
	}
	if v, ok := getDuration(m, "timeout"); ok {
		c.Timeout = v
	}
	if v, ok := getInt(m, "max_retries"); ok {
		c.MaxRetries = v
	}
	if v, ok := getDuration(m, "retry_base_delay"); ok {
		c.RetryBaseDelay = v
	}
}

func applyEngine(c *EngineConfig, m map[string]interface{}) {
	if v, ok := getInt(m, "max_turns"); ok {
		c.MaxTurns = v
	}
	if v, ok := getInt(m, "token_budget"); ok {
		c.TokenBudget = v
	}
	if v, ok := getBool(m, "auto_compact"); ok {
		c.AutoCompact = v
	}
	if v, ok := getInt(m, "max_retries"); ok {
		c.MaxRetries = v
	}
	if v, ok := getDuration(m, "retry_base_delay"); ok {
		c.RetryBaseDelay = v
	}
}

func applyTools(c *ToolsConfig, m map[string]interface{}) {
	if v, ok := getInt(m, "max_concurrency"); ok {
		c.MaxConcurrency = v
	}
	if v, ok := getInt(m, "max_result_size"); ok {
		c.MaxResultSize = v
	}
	if v, ok := getDuration(m, "timeout"); ok {
		c.Timeout = v
	}
	if v, ok := getBool(m, "use_builtin_grep"); ok {
		c.UseBuiltinGrep = v
	}
}

func applyMCP(c *MCPConfig, m map[string]interface{}) {
	if v, ok := getBool(m, "enabled"); ok {
		c.Enabled = v
	}
	if v, ok := getDuration(m, "connect_timeout"); ok {
		c.ConnectTimeout = v
	}
	if v, ok := getDuration(m, "request_timeout"); ok {
		c.RequestTimeout = v
	}
}

func applyAgentTeams(c *AgentTeamsConfig, m map[string]interface{}) {
	if v, ok := getBool(m, "enabled"); ok {
		c.Enabled = v
	}
}

func applyPermissions(c *PermissionsConfig, m map[string]interface{}) {
	if v, ok := getString(m, "mode"); ok {
		c.Mode = v
	}
	if v, ok := getBool(m, "auto_approve_read"); ok {
		c.AutoApproveRead = v
	}
}

func applySandbox(c *SandboxConfig, m map[string]interface{}) {
	if v, ok := getBool(m, "enabled"); ok {
		c.Enabled = v
	}
	if fr, ok := getMap(m, "filesystem_read"); ok {
		applySandboxFS(&c.FilesystemRead, fr)
	}
	if fw, ok := getMap(m, "filesystem_write"); ok {
		applySandboxFS(&c.FilesystemWrite, fw)
	}
	if nw, ok := getMap(m, "network"); ok {
		applySandboxNetwork(&c.Network, nw)
	}
	if v, ok := getBool(m, "allow_unsandboxed_commands"); ok {
		c.AllowUnsandboxedCommands = v
	}
	if v, ok := getStringSlice(m, "excluded_commands"); ok {
		c.ExcludedCommands = v
	}
	if v, ok := getBool(m, "enable_weaker_nested_sandbox"); ok {
		c.EnableWeakerNestedSandbox = v
	}
	if v, ok := getBool(m, "ignore_violations"); ok {
		c.IgnoreViolations = v
	}
}

func applySandboxFS(c *SandboxFSConfig, m map[string]interface{}) {
	if v, ok := getStringSlice(m, "allow"); ok {
		c.Allow = v
	}
	if v, ok := getStringSlice(m, "deny"); ok {
		c.Deny = v
	}
}

func applySandboxNetwork(c *SandboxNetworkConfig, m map[string]interface{}) {
	if v, ok := getBool(m, "disable_network"); ok {
		c.DisableNetwork = v
	}
	if v, ok := getStringSlice(m, "allowed_domains"); ok {
		c.AllowedDomains = v
	}
	if v, ok := getStringSlice(m, "denied_domains"); ok {
		c.DeniedDomains = v
	}
	if v, ok := getBool(m, "allow_unix_sockets"); ok {
		c.AllowUnixSockets = v
	}
	if v, ok := getBool(m, "allow_local_binding"); ok {
		c.AllowLocalBinding = v
	}
}

func applySession(c *SessionConfig, m map[string]interface{}) {
	if v, ok := getString(m, "history_dir"); ok {
		c.HistoryDir = v
	}
	if v, ok := getString(m, "memory_file"); ok {
		c.MemoryFile = v
	}
	if v, ok := getInt(m, "max_memory_lines"); ok {
		c.MaxMemoryLines = v
	}
	if v, ok := getInt(m, "max_memory_bytes"); ok {
		c.MaxMemoryBytes = v
	}
}

func applyTUI(c *TUIConfig, m map[string]interface{}) {
	if v, ok := getString(m, "theme"); ok {
		c.Theme = v
	}
	if v, ok := getBool(m, "show_token_count"); ok {
		c.ShowTokenCount = v
	}
	if v, ok := getBool(m, "show_cost"); ok {
		c.ShowCost = v
	}
}

func applySystemPrompt(c *SystemPromptConfig, m map[string]interface{}) {
	if v, ok := getString(m, "guidelines"); ok {
		c.Guidelines = v
	}
	if v, ok := getString(m, "subagent_mode"); ok {
		c.SubagentMode = v
	}
	if v, ok := getString(m, "team_mode"); ok {
		c.TeamMode = v
	}
	if v, ok := getString(m, "extra"); ok {
		c.Extra = v
	}
}

func applyWorkspace(c *WorkspaceConfig, m map[string]interface{}) {
	if v, ok := getString(m, "dir"); ok {
		c.Dir = v
	}
	if v, ok := getBool(m, "auto_create"); ok {
		c.AutoCreate = v
	}
}

func applyLongTermMemory(c *LongTermMemoryConfig, m map[string]interface{}) {
	if v, ok := getBool(m, "enabled"); ok {
		c.Enabled = v
	}
	if v, ok := getString(m, "db_path"); ok {
		c.DBPath = v
	}
	if capt, ok := getMap(m, "capture"); ok {
		applyLongTermCapture(&c.Capture, capt)
	}
	if inj, ok := getMap(m, "injection"); ok {
		applyLongTermInjection(&c.Injection, inj)
	}
	if cap, ok := getMap(m, "capacity"); ok {
		applyLongTermCapacity(&c.Capacity, cap)
	}
	if ev, ok := getMap(m, "eviction"); ok {
		applyLongTermEviction(&c.Eviction, ev)
	}
	if exp, ok := getMap(m, "expiration"); ok {
		applyLongTermExpiration(&c.Expiration, exp)
	}
	if priv, ok := getMap(m, "privacy"); ok {
		applyLongTermPrivacy(&c.Privacy, priv)
	}
}

func applyLongTermCapture(c *LongTermCaptureConfig, m map[string]interface{}) {
	if v, ok := getBool(m, "auto_capture_tools"); ok {
		c.AutoCaptureTools = v
	}
	if v, ok := getInt(m, "max_observation_size"); ok {
		c.MaxObservationSize = v
	}
	if v, ok := getInt(m, "min_capture_chars"); ok {
		c.MinCaptureChars = v
	}
}

func applyLongTermInjection(c *LongTermInjectionConfig, m map[string]interface{}) {
	if v, ok := getBool(m, "auto_inject"); ok {
		c.AutoInject = v
	}
	if v, ok := getInt(m, "max_inject_tokens"); ok {
		c.MaxInjectTokens = v
	}
	if v, ok := getInt(m, "search_limit"); ok {
		c.SearchLimit = v
	}
	if v, ok := getFloat(m, "min_relevance_score"); ok {
		c.MinRelevanceScore = v
	}
}

func applyLongTermCapacity(c *LongTermCapacityConfig, m map[string]interface{}) {
	if v, ok := getInt(m, "max_entries"); ok {
		c.MaxEntries = v
	}
	if v, ok := getInt(m, "max_storage_bytes"); ok {
		c.MaxStorageBytes = v
	}
}

func applyLongTermEviction(c *LongTermEvictionConfig, m map[string]interface{}) {
	if v, ok := getString(m, "policy"); ok {
		c.Policy = v
	}
	if v, ok := getBool(m, "auto_summarize"); ok {
		c.AutoSummarize = v
	}
	if v, ok := getInt(m, "min_priority"); ok {
		c.MinPriority = v
	}
}

func applyLongTermExpiration(c *LongTermExpirationConfig, m map[string]interface{}) {
	if v, ok := getInt(m, "default_ttl_days"); ok {
		c.DefaultTTLDays = v
	}
	if v, ok := getInt(m, "low_priority_ttl_days"); ok {
		c.LowPriorityTTLDays = v
	}
	if v, ok := getInt(m, "cleanup_interval_hours"); ok {
		c.CleanupIntervalHours = v
	}
}

func applyLongTermPrivacy(c *LongTermPrivacyConfig, m map[string]interface{}) {
	if v, ok := getBool(m, "auto_exclude_patterns"); ok {
		c.AutoExcludePatterns = v
	}
	if v, ok := getBool(m, "strip_private_tags"); ok {
		c.StripPrivateTags = v
	}
}

// ----- 类型萃取辅助 -----

func getMap(m map[string]interface{}, key string) (map[string]interface{}, bool) {
	v, ok := m[key]
	if !ok {
		return nil, false
	}
	mm, ok := v.(map[string]interface{})
	return mm, ok
}

func getStringSlice(m map[string]interface{}, key string) ([]string, bool) {
	v, ok := m[key]
	if !ok {
		return nil, false
	}
	switch arr := v.(type) {
	case []string:
		return arr, true
	case []interface{}:
		out := make([]string, 0, len(arr))
		for _, e := range arr {
			if s, ok := e.(string); ok {
				out = append(out, s)
			} else {
				out = append(out, fmt.Sprintf("%v", e))
			}
		}
		return out, true
	}
	return nil, false
}

func getString(m map[string]interface{}, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	switch s := v.(type) {
	case string:
		return s, true
	case int64:
		return strconv.FormatInt(s, 10), true
	case float64:
		return strconv.FormatFloat(s, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(s), true
	}
	return fmt.Sprintf("%v", v), true
}

func getInt(m map[string]interface{}, key string) (int, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int64:
		return int(n), true
	case int:
		return n, true
	case float64:
		return int(n), true
	case string:
		if x, err := strconv.Atoi(n); err == nil {
			return x, true
		}
	}
	return 0, false
}

func getFloat(m map[string]interface{}, key string) (float64, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case string:
		if x, err := strconv.ParseFloat(n, 64); err == nil {
			return x, true
		}
	}
	return 0, false
}

func getBool(m map[string]interface{}, key string) (bool, bool) {
	v, ok := m[key]
	if !ok {
		return false, false
	}
	if b, ok := v.(bool); ok {
		return b, true
	}
	if s, ok := v.(string); ok {
		switch s {
		case "true", "yes", "on", "1":
			return true, true
		case "false", "no", "off", "0":
			return false, true
		}
	}
	return false, false
}

// getDuration 解析 "300s" / "1h" / 数字（按秒）/ 字符串数字
func getDuration(m map[string]interface{}, key string) (time.Duration, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case string:
		if d, err := time.ParseDuration(n); err == nil {
			return d, true
		}
		if x, err := strconv.Atoi(n); err == nil {
			return time.Duration(x) * time.Second, true
		}
	case int64:
		return time.Duration(n) * time.Second, true
	case int:
		return time.Duration(n) * time.Second, true
	case float64:
		return time.Duration(n * float64(time.Second)), true
	}
	return 0, false
}

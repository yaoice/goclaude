// Package workflow 提供 workflow 定义文件的 YAML / JSON 加载能力。
//
// 对齐 oh-my-openagent 的 agent/category 加载模式，
// 从用户目录和项目目录加载 *.yaml / *.yml / *.json workflow 定义文件。
//
// 格式选择原则：
//   - YAML: 人类编写友好，支持注释（*.yaml, *.yml）
//   - JSON:  AI/程序生成友好，严格语法（*.json）
//     Plan Agent 自动生成的定义文件统一使用 JSON 格式（AI 输出可靠性更高）。
package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yaoice/goclaude/pkg/domain/workflow"
	"gopkg.in/yaml.v3"
)

// Loader workflow 定义加载器。
//
// 从以下目录按优先级加载：
//  1. 项目目录:  {workdir}/.goclaude/workflows/*.yaml
//  2. 用户目录:  ~/.goclaude/workflows/*.yaml
//
// 同名 workflow 以项目目录优先（允许项目覆盖用户全局定义）。
type Loader struct {
	// userDir 用户级 workflow 目录（~/.goclaude/workflows）
	userDir string
}

// NewLoader 创建 Loader。
//
// homeDir: 用户 Home 目录（通常为 os.UserHomeDir() 的结果）
func NewLoader(homeDir string) *Loader {
	return &Loader{
		userDir: filepath.Join(homeDir, ".goclaude", "workflows"),
	}
}

// Load 从项目目录和用户目录加载所有 workflow 定义。
//
// 同名 workflow（Name 字段）以 projectDir 优先。
// projectDir 传空时仅从用户目录加载。
func (l *Loader) Load(projectDir string) ([]*workflow.Workflow, error) {
	var all []*workflow.Workflow
	seen := make(map[string]bool)

	// 先加载用户目录（低优先级）
	if l.userDir != "" {
		userWfs, err := l.loadDir(l.userDir)
		if err != nil {
			// 用户目录不存在不是致命错误
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("load user workflows from %s: %w", l.userDir, err)
			}
		}
		for _, wf := range userWfs {
			if !seen[wf.Name] {
				all = append(all, wf)
				seen[wf.Name] = true
			}
		}
	}

	// 再加载项目目录（高优先级，覆盖同名）
	if projectDir != "" {
		projectWFPath := filepath.Join(projectDir, ".goclaude", "workflows")
		projectWfs, err := l.loadDir(projectWFPath)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("load project workflows from %s: %w", projectWFPath, err)
			}
		}
		for _, wf := range projectWfs {
			// 项目目录覆盖同名
			if seen[wf.Name] {
				// 替换已有
				for i, existing := range all {
					if existing.Name == wf.Name {
						all[i] = wf
						break
					}
				}
			} else {
				all = append(all, wf)
				seen[wf.Name] = true
			}
		}
	}

	return all, nil
}

// LoadByName 按名称加载单个 workflow。
//
// 搜索顺序：projectDir > userDir。
// 每种位置尝试 .yaml → .yml → .json 三种扩展名。
// 返回 error 当文件不存在或解析失败。
func (l *Loader) LoadByName(name string, projectDir string) (*workflow.Workflow, error) {
	// 优先项目目录
	if projectDir != "" {
		base := filepath.Join(projectDir, ".goclaude", "workflows", name)
		for _, ext := range []string{".yaml", ".yml", ".json"} {
			wf, err := l.loadFile(base + ext)
			if err == nil {
				return wf, nil
			}
			if !os.IsNotExist(err) {
				return nil, err
			}
		}
	}

	// 回退到用户目录
	if l.userDir != "" {
		base := filepath.Join(l.userDir, name)
		for _, ext := range []string{".yaml", ".yml", ".json"} {
			wf, err := l.loadFile(base + ext)
			if err == nil {
				return wf, nil
			}
			if !os.IsNotExist(err) {
				return nil, err
			}
		}
	}

	return nil, fmt.Errorf("workflow %q not found (searched project and user workflow directories)", name)
}

// loadDir 加载目录下所有 .yaml/.yml/.json 文件
func (l *Loader) loadDir(dir string) ([]*workflow.Workflow, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var result []*workflow.Workflow
	supportedExts := map[string]bool{".yaml": true, ".yml": true, ".json": true}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if !supportedExts[ext] {
			continue
		}

		path := filepath.Join(dir, name)
		wf, err := l.loadFile(path)
		if err != nil {
			// 记录警告但继续加载其他文件
			fmt.Fprintf(os.Stderr, "Warning: skip workflow file %s: %v\n", path, err)
			continue
		}

		// 如果文件中没有显式指定 Name，从文件名推导
		if wf.Name == "" {
			wf.Name = strings.TrimSuffix(name, ext)
		}
		result = append(result, wf)
	}

	return result, nil
}

// loadFile 解析单个 YAML 或 JSON 文件，根据扩展名自动选择解析器。
func (l *Loader) loadFile(path string) (*workflow.Workflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	ext := strings.ToLower(filepath.Ext(path))
	var wf workflow.Workflow

	switch ext {
	case ".json":
		if err := json.Unmarshal(data, &wf); err != nil {
			return nil, fmt.Errorf("parse workflow JSON %s: %w", path, err)
		}
	default: // .yaml, .yml
		if err := yaml.Unmarshal(data, &wf); err != nil {
			return nil, fmt.Errorf("parse workflow YAML %s: %w", path, err)
		}
	}

	return &wf, nil
}

// Save 将 workflow 定义序列化并保存到项目目录。
// format: "yaml" 或 "json"（默认 json，AI 生成友好）。
func (l *Loader) Save(projectDir string, wf *workflow.Workflow, format string) (string, error) {
	if wf.Name == "" {
		return "", fmt.Errorf("workflow name is required for saving")
	}

	dir := filepath.Join(projectDir, ".goclaude", "workflows")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create workflow dir: %w", err)
	}

	var ext string
	var data []byte
	var err error

	switch format {
	case "yaml", "yml":
		ext = ".yaml"
		data, err = yaml.Marshal(wf)
	default:
		ext = ".json"
		// JSON 格式化输出，便于人类审查
		data, err = json.MarshalIndent(wf, "", "  ")
	}
	if err != nil {
		return "", fmt.Errorf("marshal workflow: %w", err)
	}

	path := filepath.Join(dir, wf.Name+ext)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("write workflow file: %w", err)
	}

	return path, nil
}

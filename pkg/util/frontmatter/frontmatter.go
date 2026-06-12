// Package frontmatter 解析 markdown 文件头部 YAML frontmatter
//
// 对齐 src/utils/frontmatterParser.ts 的核心语义，但只覆盖 skills/agents
// 真正使用的字段类型（字符串、布尔、字符串数组），不引入完整 YAML 依赖。
package frontmatter

import (
	"bufio"
	"fmt"
	"strings"
)

// Data 解析后的 frontmatter 数据（保留原始字段名，值为 string / []string / bool / map）
type Data map[string]any

// Parse 从 markdown 内容中分离 frontmatter 与正文。
// 若内容不以 --- 开头则 frontmatter 为空、content 为原始内容。
func Parse(content string) (Data, string, error) {
	if !strings.HasPrefix(content, "---") {
		return Data{}, content, nil
	}

	// 去掉前导的 ---\n 或 ---\r\n
	rest := content[3:]
	rest = strings.TrimLeft(rest, "\r\n")

	// 在剩余内容中找到下一个 \n---\n（或 \n---EOF）
	endIdx := findFrontmatterEnd(rest)
	if endIdx < 0 {
		// 没找到结束符，按无 frontmatter 处理
		return Data{}, content, nil
	}

	fmText := rest[:endIdx]
	// 跳过结束行 ---\n
	body := strings.TrimLeft(rest[endIdx:], "\r\n")
	body = strings.TrimPrefix(body, "---")
	body = strings.TrimLeft(body, "\r\n")

	data, err := parseYAMLSubset(fmText)
	if err != nil {
		return nil, "", err
	}
	return data, body, nil
}

// findFrontmatterEnd 寻找 \n---\n 或 \n---<EOF> 的索引
func findFrontmatterEnd(s string) int {
	lines := strings.Split(s, "\n")
	offset := 0
	for _, line := range lines {
		trimmed := strings.TrimRight(line, "\r")
		if trimmed == "---" {
			return offset
		}
		offset += len(line) + 1 // 包含换行
	}
	return -1
}

// parseYAMLSubset 极简 YAML 解析（仅支持 skills/agents 使用的字段形态）：
//   - key: value         （标量）
//   - key: "value"       （带引号的标量）
//   - key: [a, b, c]     （行内数组）
//   - key:               （后续行 - value 形式的数组）
//   - item1
//   - item2
//   - key: |             （多行字符串：折叠/字面均按字面处理）
//     line1
//     line2
//
// 不支持嵌套 map 等复杂结构。未识别的复杂值会保留为原始字符串。
func parseYAMLSubset(text string) (Data, error) {
	out := Data{}
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var (
		currentKey     string
		currentArr     []string
		currentLiteral *strings.Builder
		literalIndent  int
	)

	flush := func() {
		if currentKey == "" {
			return
		}
		switch {
		case currentArr != nil:
			out[currentKey] = currentArr
		case currentLiteral != nil:
			out[currentKey] = strings.TrimRight(currentLiteral.String(), "\n")
		}
		currentKey = ""
		currentArr = nil
		currentLiteral = nil
		literalIndent = -1
	}

	for scanner.Scan() {
		raw := scanner.Text()
		// 多行字面值收集（| / > 块）
		//
		// literalIndent 语义：
		//   -1 = 尚未校准基线（刚进入字面值块，等待首条非空行）
		//   >0 = 已固定基线，所有内容行必须 >= 基线
		if currentLiteral != nil {
			trimmed := strings.TrimSpace(raw)
			if trimmed == "" {
				currentLiteral.WriteString("\n")
				continue
			}
			indent := leadingSpaces(raw)
			// 首次校准：用第一条非空行的缩进作为基线
			if literalIndent < 0 {
				if indent == 0 {
					// 第一行就完全顶格，说明字面值已结束（YAML 不允许 0 缩进延续 | 块）
					flush()
					// 落到下方主循环，把当前行重新当作 key 行解析
				} else {
					literalIndent = indent
					currentLiteral.WriteString(raw[indent:])
					currentLiteral.WriteString("\n")
					continue
				}
			} else if indent >= literalIndent {
				currentLiteral.WriteString(raw[literalIndent:])
				currentLiteral.WriteString("\n")
				continue
			} else {
				// 缩进不足，字面值块结束；当前行不属于字面值，重走主循环
				flush()
			}
		}

		line := raw
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		// 注释
		if strings.HasPrefix(trim, "#") {
			continue
		}

		// "- value" 形式的数组项（属于上一个 key）
		if strings.HasPrefix(trim, "- ") && currentKey != "" && currentArr != nil {
			item := strings.TrimSpace(trim[2:])
			item = unquote(item)
			currentArr = append(currentArr, item)
			continue
		}

		// "key: ..." 或 "key:"
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		// 之前积累的内容先 flush
		flush()

		key := strings.TrimSpace(line[:colon])
		value := strings.TrimSpace(line[colon+1:])
		key = unquote(key)

		switch {
		case value == "":
			// 后续会是数组或多行
			currentKey = key
			currentArr = []string{}
		case value == "|" || value == ">":
			// 进入多行字面值收集；用 -1 表示"等待首行校准基线"
			currentKey = key
			currentLiteral = &strings.Builder{}
			literalIndent = -1
		case strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]"):
			// 行内数组
			inner := value[1 : len(value)-1]
			parts := splitCSV(inner)
			arr := make([]string, 0, len(parts))
			for _, p := range parts {
				arr = append(arr, unquote(strings.TrimSpace(p)))
			}
			out[key] = arr
		default:
			out[key] = unquote(value)
		}
	}
	// flush 尾部
	flush()

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan frontmatter: %w", err)
	}
	return out, nil
}

func leadingSpaces(s string) int {
	n := 0
	for _, r := range s {
		if r == ' ' {
			n++
		} else {
			break
		}
	}
	return n
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// splitCSV 按逗号切分，跳过引号内的逗号
func splitCSV(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	quoteChar := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote {
			if c == quoteChar {
				inQuote = false
			}
			cur.WriteByte(c)
			continue
		}
		if c == '"' || c == '\'' {
			inQuote = true
			quoteChar = c
			cur.WriteByte(c)
			continue
		}
		if c == ',' {
			out = append(out, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// GetString 安全取 string
func GetString(d Data, key string) string {
	if v, ok := d[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// GetBool 安全取 bool（支持 "true"/"false"/true/false）
func GetBool(d Data, key string) bool {
	if v, ok := d[key]; ok {
		switch x := v.(type) {
		case bool:
			return x
		case string:
			return strings.EqualFold(x, "true")
		}
	}
	return false
}

// GetBoolPtr 取可选 bool，未设置返回 nil
func GetBoolPtr(d Data, key string) *bool {
	v, ok := d[key]
	if !ok {
		return nil
	}
	var b bool
	switch x := v.(type) {
	case bool:
		b = x
	case string:
		if strings.EqualFold(x, "true") {
			b = true
		} else if strings.EqualFold(x, "false") {
			b = false
		} else {
			return nil
		}
	default:
		return nil
	}
	return &b
}

// GetStringSlice 取 []string；支持 "a, b, c" 字符串形式
func GetStringSlice(d Data, key string) []string {
	v, ok := d[key]
	if !ok {
		return nil
	}
	switch x := v.(type) {
	case []string:
		return x
	case string:
		parts := strings.Split(x, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			t := strings.TrimSpace(p)
			if t != "" {
				out = append(out, t)
			}
		}
		return out
	}
	return nil
}

// Has 是否存在指定 key
func Has(d Data, key string) bool {
	_, ok := d[key]
	return ok
}

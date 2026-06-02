package shell

import (
	"strings"
	"unicode"
)

// 轻量语法高亮器
//
// 设计取舍：
//   - 不依赖 chroma / glamour 等大库，保持 go 工程零外部依赖
//   - 每种语言只做"足够用"的 lex：keyword / string / number / comment
//   - 未识别的语言回退到调用方的"整块灰"逻辑
//
// 入口：HighlightLine(line, lang) → 返回带 ANSI 转义的字符串

// 颜色（终端通用 SGR）
const (
	hlKeyword = "\x1b[35m"   // 紫
	hlString  = "\x1b[32m"   // 绿
	hlNumber  = "\x1b[36m"   // 青
	hlComment = "\x1b[2;37m" // dim 灰
	hlType    = "\x1b[33m"   // 黄
	hlPunct   = "\x1b[37m"   // 浅灰
	hlReset   = "\x1b[0m"
)

// 各语言关键字集合（小集合够用；更精确的可后续扩展）
var hlKeywords = map[string]map[string]struct{}{
	"go": setOf(
		"package", "import", "func", "var", "const", "type", "struct", "interface",
		"map", "chan", "go", "defer", "select", "case", "switch", "return",
		"if", "else", "for", "range", "break", "continue", "fallthrough",
		"true", "false", "nil", "iota", "goto",
	),
	"typescript": setOf(
		"const", "let", "var", "function", "class", "interface", "type", "enum",
		"return", "if", "else", "for", "while", "do", "switch", "case", "break",
		"continue", "import", "export", "from", "default", "extends", "implements",
		"public", "private", "protected", "static", "readonly", "async", "await",
		"new", "this", "super", "throw", "try", "catch", "finally", "true", "false", "null", "undefined",
	),
	"javascript": setOf(
		"const", "let", "var", "function", "class", "return", "if", "else",
		"for", "while", "do", "switch", "case", "break", "continue", "import",
		"export", "from", "default", "extends", "new", "this", "super", "throw",
		"try", "catch", "finally", "async", "await", "true", "false", "null", "undefined",
	),
	"python": setOf(
		"def", "class", "return", "if", "elif", "else", "for", "while", "break",
		"continue", "pass", "import", "from", "as", "with", "try", "except",
		"finally", "raise", "yield", "lambda", "global", "nonlocal",
		"True", "False", "None", "and", "or", "not", "is", "in",
	),
	"rust": setOf(
		"fn", "let", "mut", "const", "struct", "enum", "trait", "impl", "pub",
		"use", "mod", "match", "if", "else", "for", "while", "loop", "break",
		"continue", "return", "self", "Self", "super", "as", "where", "ref",
		"async", "await", "move", "true", "false",
	),
	"java": setOf(
		"public", "private", "protected", "class", "interface", "extends", "implements",
		"static", "final", "abstract", "void", "int", "long", "boolean", "String",
		"new", "return", "if", "else", "for", "while", "do", "switch", "case",
		"break", "continue", "throw", "throws", "try", "catch", "finally",
		"package", "import", "this", "super", "true", "false", "null",
	),
	"sh": setOf(
		"if", "then", "fi", "elif", "else", "for", "while", "do", "done",
		"case", "esac", "in", "function", "return", "exit", "break", "continue",
		"export", "local", "readonly", "true", "false",
	),
}

// 行注释起始符（语言别名映射到统一表）
var hlLineComment = map[string]string{
	"go": "//", "typescript": "//", "javascript": "//", "ts": "//", "js": "//",
	"rust": "//", "java": "//", "c": "//", "cpp": "//", "c++": "//",
	"python": "#", "py": "#", "sh": "#", "bash": "#", "shell": "#",
	"yaml": "#", "yml": "#", "toml": "#", "ini": "#",
}

// hlLangAlias 把短别名归一到 hlKeywords 的主键
var hlLangAlias = map[string]string{
	"ts":    "typescript",
	"js":    "javascript",
	"py":    "python",
	"bash":  "sh",
	"shell": "sh",
	"c++":   "cpp",
}

// HighlightLine 给一行代码加 ANSI 着色
//
//	lang 取自围栏 ```<lang>；空 / 未识别 → 返回原串
//	已 trim 行尾 \r\n，不会主动添加
//
// 简化的 lex 流程：
//  1. 如果整行命中行注释起始 → 全行染 dim 灰
//  2. 否则按字符走：字符串 → 数字 → 标识符（关键字判定）→ 其它
func HighlightLine(line, lang string) string {
	if line == "" {
		return ""
	}
	lang = strings.ToLower(strings.TrimSpace(lang))
	if alias, ok := hlLangAlias[lang]; ok {
		lang = alias
	}

	// JSON / YAML 用专门的 highlighter
	switch lang {
	case "json":
		return highlightJSON(line)
	case "yaml", "yml":
		return highlightYAML(line)
	}

	keywords := hlKeywords[lang]
	if keywords == nil {
		// 未支持的语言：原样返回（调用方决定回退色）
		return line
	}

	// 行注释优先
	if cmt, ok := hlLineComment[lang]; ok {
		if i := strings.Index(line, cmt); i >= 0 && !insideQuoteBefore(line, i) {
			head := highlightCodeNoComment(line[:i], keywords)
			return head + hlComment + line[i:] + hlReset
		}
	}
	return highlightCodeNoComment(line, keywords)
}

// highlightCodeNoComment 按 token 染色（不处理行注释；caller 已切掉）
func highlightCodeNoComment(s string, keywords map[string]struct{}) string {
	var sb strings.Builder
	runes := []rune(s)
	i := 0
	for i < len(runes) {
		r := runes[i]
		// 字符串
		if r == '"' || r == '\'' || r == '`' {
			j := scanString(runes, i)
			sb.WriteString(hlString)
			sb.WriteString(string(runes[i:j]))
			sb.WriteString(hlReset)
			i = j
			continue
		}
		// 数字
		if isDigit(r) || (r == '.' && i+1 < len(runes) && isDigit(runes[i+1])) {
			j := scanNumber(runes, i)
			sb.WriteString(hlNumber)
			sb.WriteString(string(runes[i:j]))
			sb.WriteString(hlReset)
			i = j
			continue
		}
		// 标识符 / 关键字
		if unicode.IsLetter(r) || r == '_' {
			j := scanIdent(runes, i)
			word := string(runes[i:j])
			if _, ok := keywords[word]; ok {
				sb.WriteString(hlKeyword)
				sb.WriteString(word)
				sb.WriteString(hlReset)
			} else if isLikelyType(word) {
				sb.WriteString(hlType)
				sb.WriteString(word)
				sb.WriteString(hlReset)
			} else {
				sb.WriteString(word)
			}
			i = j
			continue
		}
		// 其它字符原样
		sb.WriteRune(r)
		i++
	}
	return sb.String()
}

// scanString 从 i 处的引号扫到匹配的引号或行尾
func scanString(runes []rune, i int) int {
	q := runes[i]
	j := i + 1
	for j < len(runes) {
		c := runes[j]
		if c == '\\' && j+1 < len(runes) {
			j += 2
			continue
		}
		j++
		if c == q {
			return j
		}
	}
	return len(runes)
}

// scanNumber 简单整数 / 浮点 / 0x 前缀
func scanNumber(runes []rune, i int) int {
	j := i
	if j+1 < len(runes) && runes[j] == '0' && (runes[j+1] == 'x' || runes[j+1] == 'X') {
		j += 2
		for j < len(runes) && isHex(runes[j]) {
			j++
		}
		return j
	}
	for j < len(runes) && (isDigit(runes[j]) || runes[j] == '.' || runes[j] == '_') {
		j++
	}
	// e/E 指数
	if j < len(runes) && (runes[j] == 'e' || runes[j] == 'E') {
		j++
		if j < len(runes) && (runes[j] == '+' || runes[j] == '-') {
			j++
		}
		for j < len(runes) && isDigit(runes[j]) {
			j++
		}
	}
	return j
}

// scanIdent 识别 [A-Za-z_][A-Za-z0-9_]*
func scanIdent(runes []rune, i int) int {
	j := i
	for j < len(runes) {
		r := runes[j]
		if unicode.IsLetter(r) || r == '_' || (j > i && unicode.IsDigit(r)) {
			j++
			continue
		}
		break
	}
	return j
}

// isLikelyType 启发式：以大写字母开头的标识符（go/ts/rust 习惯）
func isLikelyType(s string) bool {
	if s == "" {
		return false
	}
	r := []rune(s)[0]
	return unicode.IsUpper(r)
}

// insideQuoteBefore 判断 idx 之前是否在未关闭的字符串中（避免把字符串内 // 当注释）
func insideQuoteBefore(s string, idx int) bool {
	inStr := byte(0)
	for i := 0; i < idx; i++ {
		c := s[i]
		if inStr != 0 {
			if c == '\\' && i+1 < idx {
				i++
				continue
			}
			if c == inStr {
				inStr = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			inStr = c
		}
	}
	return inStr != 0
}

// highlightJSON 简化 JSON：key:value、字符串、数字、true/false/null
func highlightJSON(s string) string {
	var sb strings.Builder
	runes := []rune(s)
	i := 0
	for i < len(runes) {
		r := runes[i]
		if r == '"' {
			j := scanString(runes, i)
			// 判断是否 key（后面紧跟 :）
			isKey := false
			k := j
			for k < len(runes) && (runes[k] == ' ' || runes[k] == '\t') {
				k++
			}
			if k < len(runes) && runes[k] == ':' {
				isKey = true
			}
			color := hlString
			if isKey {
				color = hlType
			}
			sb.WriteString(color)
			sb.WriteString(string(runes[i:j]))
			sb.WriteString(hlReset)
			i = j
			continue
		}
		if isDigit(r) || (r == '-' && i+1 < len(runes) && isDigit(runes[i+1])) {
			j := i
			if r == '-' {
				j++
			}
			for j < len(runes) && (isDigit(runes[j]) || runes[j] == '.' || runes[j] == 'e' || runes[j] == 'E' || runes[j] == '+' || runes[j] == '-') {
				j++
			}
			sb.WriteString(hlNumber)
			sb.WriteString(string(runes[i:j]))
			sb.WriteString(hlReset)
			i = j
			continue
		}
		if unicode.IsLetter(r) {
			j := scanIdent(runes, i)
			word := string(runes[i:j])
			switch word {
			case "true", "false", "null":
				sb.WriteString(hlKeyword)
				sb.WriteString(word)
				sb.WriteString(hlReset)
			default:
				sb.WriteString(word)
			}
			i = j
			continue
		}
		sb.WriteRune(r)
		i++
	}
	return sb.String()
}

// highlightYAML 简化 YAML：key: value、注释 #、字符串、数字
func highlightYAML(s string) string {
	// 行注释
	if i := strings.Index(s, "#"); i >= 0 && !insideQuoteBefore(s, i) {
		return highlightYAMLNoComment(s[:i]) + hlComment + s[i:] + hlReset
	}
	return highlightYAMLNoComment(s)
}

func highlightYAMLNoComment(s string) string {
	// 找 key: 模式（行首缩进 + 标识符 + ":"）
	trimmed := strings.TrimLeft(s, " \t")
	indent := s[:len(s)-len(trimmed)]
	// 检查 "- " 列表前缀
	listMark := ""
	if strings.HasPrefix(trimmed, "- ") {
		listMark = trimmed[:2]
		trimmed = trimmed[2:]
	}
	// 找冒号位置
	colon := strings.Index(trimmed, ":")
	if colon < 0 {
		return indent + listMark + highlightYAMLValue(trimmed)
	}
	key := trimmed[:colon]
	rest := trimmed[colon:]
	// key 必须是合法标识符
	if !isYAMLKey(key) {
		return indent + listMark + highlightYAMLValue(trimmed)
	}
	value := ""
	if len(rest) > 1 {
		value = rest[1:]
	}
	out := indent + listMark + hlType + key + hlReset + ":" + highlightYAMLValue(value)
	return out
}

// yamlSpecialValues YAML 中不带引号的"特殊值"集合
var yamlSpecialValues = map[string]struct{}{
	"true": {}, "false": {}, "null": {},
	"True": {}, "False": {}, "None": {},
	"yes": {}, "no": {},
}

func highlightYAMLValue(v string) string {
	// 字符串引号
	v = strings.TrimRight(v, "\r")
	trimmed := strings.TrimLeft(v, " ")
	prefix := v[:len(v)-len(trimmed)]
	if trimmed == "" {
		return v
	}
	// 引号字符串
	if trimmed[0] == '"' || trimmed[0] == '\'' {
		runes := []rune(trimmed)
		j := scanString(runes, 0)
		return prefix + hlString + string(runes[:j]) + hlReset + string(runes[j:])
	}
	// 数字
	if isDigit(rune(trimmed[0])) || (trimmed[0] == '-' && len(trimmed) > 1 && isDigit(rune(trimmed[1]))) {
		j := 0
		runes := []rune(trimmed)
		if runes[0] == '-' {
			j++
		}
		for j < len(runes) && (isDigit(runes[j]) || runes[j] == '.') {
			j++
		}
		return prefix + hlNumber + string(runes[:j]) + hlReset + string(runes[j:])
	}
	// true/false/null 等关键字
	if _, ok := yamlSpecialValues[trimmed]; ok {
		return prefix + hlKeyword + trimmed + hlReset
	}
	return v
}

func isYAMLKey(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

// utils
func setOf(words ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(words))
	for _, w := range words {
		m[w] = struct{}{}
	}
	return m
}
func isDigit(r rune) bool { return r >= '0' && r <= '9' }
func isHex(r rune) bool   { return isDigit(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') }

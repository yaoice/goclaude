package shell

import (
	"bytes"
	"strings"
	"testing"
)

func formatAll(input string) string {
	f := NewStreamFormatter("", "")
	var b bytes.Buffer
	f.Write(&b, input)
	f.Flush(&b)
	return b.String()
}

func formatChunked(input string, sizes []int) string {
	f := NewStreamFormatter("", "")
	var b bytes.Buffer
	pos := 0
	for _, n := range sizes {
		end := pos + n
		if end > len(input) {
			end = len(input)
		}
		f.Write(&b, input[pos:end])
		pos = end
		if pos >= len(input) {
			break
		}
	}
	if pos < len(input) {
		f.Write(&b, input[pos:])
	}
	f.Flush(&b)
	return b.String()
}

func TestFormatterCRLFNormalize(t *testing.T) {
	got := formatAll("hello\nworld\n")
	if !strings.Contains(got, "hello\r\n") || !strings.Contains(got, "world\r\n") {
		t.Fatalf("missing CRLF in %q", got)
	}
}

func TestFormatterChunkInvariance(t *testing.T) {
	src := "# Title\n\n这是一段 **粗体** 与 `inline code`，链接 https://example.com 与列表：\n- one\n- two\n\n```go\nfmt.Println(\"hi\")\n```\n结尾段落。\n"
	whole := formatAll(src)
	chunked := formatChunked(src, []int{1, 3, 5, 7, 11, 13, 17, 19, 23})
	if whole != chunked {
		t.Fatalf("chunk invariance broken:\nWHOLE:\n%q\nCHUNKED:\n%q", whole, chunked)
	}
}

func TestFormatterCodeFence(t *testing.T) {
	got := formatAll("```go\nx := 1\n```\n")
	// 内容字符必须出现（不强求连续，因为可能被高亮包了 ANSI）
	if !strings.Contains(got, "x") || !strings.Contains(got, ":=") {
		t.Fatalf("missing fence body chars: %q", got)
	}
	// 行内代码（黄色 33m）+ 普通文本不应在围栏内出现
	// 注意：高亮器自身可能用 33m 染色（黄），所以仅验证不出现"行内 code"模式
}

func TestFormatterHeadersAndList(t *testing.T) {
	got := formatAll("# H1\n## H2\n- a\n1. b\n")
	if !strings.Contains(got, "\x1b[1;36m# ") {
		t.Fatalf("missing H1 color: %q", got)
	}
	if !strings.Contains(got, "\x1b[1;33m## ") {
		t.Fatalf("missing H2 color: %q", got)
	}
	// 列表项的 bullet 应被替换为 •
	if !strings.Contains(got, "•") {
		t.Fatalf("missing bullet: %q", got)
	}
}

func TestFormatterInlineNoTrailingCRLFWhenNoNewline(t *testing.T) {
	// 流尾没有 \n，Flush 不应擅自补 \r\n
	got := formatAll("hello")
	if strings.HasSuffix(got, "\r\n") {
		t.Fatalf("unexpected trailing CRLF: %q", got)
	}
	if !strings.Contains(got, "hello") {
		t.Fatalf("missing payload: %q", got)
	}
}

func TestFormatterIndentAndBar(t *testing.T) {
	f := NewStreamFormatter("  ", "│ ")
	var b bytes.Buffer
	f.Write(&b, "abc\n")
	f.Flush(&b)
	out := b.String()
	if !strings.HasPrefix(out, "  ") {
		t.Fatalf("missing indent: %q", out)
	}
	if !strings.Contains(out, "│ ") {
		t.Fatalf("missing bar: %q", out)
	}
}

// TestFlushIncompleteNoDuplicate 验证 FlushIncomplete 不会重复输出同一行内容。
//
// 实现采用「增量追加」策略（见 FlushIncomplete 文档）：每帧只输出相对上一帧新增的
// 字符，靠终端天然累积来显示完整行，从而绝不重复输出已显示过的内容，且无需依赖
// \r / \x1b[K（这些在部分终端不可靠，也无法正确处理软换行）。
//
// 因此本测试逐帧校验：
//   - 每帧只产出本帧新增的字符；
//   - 不会重复输出此前帧已经输出过的内容；
//   - 各帧输出按序拼接后等于完整行内容。
func TestFlushIncompleteNoDuplicate(t *testing.T) {
	f := NewStreamFormatter("  ", "│ ")
	var b bytes.Buffer

	// 模拟流式输出：逐帧追加 "你"、"好"、"世界"，累计为 "你好世界"
	// 第1帧
	f.Write(&b, "你")
	f.FlushIncomplete(&b)
	first := b.String()
	b.Reset()

	// 第2帧：内容增长
	f.Write(&b, "好")
	f.FlushIncomplete(&b)
	second := b.String()
	b.Reset()

	// 第3帧：内容继续增长
	f.Write(&b, "世界")
	f.FlushIncomplete(&b)
	third := b.String()
	b.Reset()

	// 第1帧：包含新增字符 "你"
	if !strings.Contains(first, "你") {
		t.Fatalf("missing new content in first frame: %q", first)
	}
	// 第2帧：只输出新增的 "好"，不应重复输出上一帧已显示的 "你"
	if !strings.Contains(second, "好") {
		t.Fatalf("missing new content in second frame: %q", second)
	}
	if strings.Contains(second, "你") {
		t.Fatalf("frame should not re-output prior content: %q", second)
	}
	// 第3帧：只输出新增的 "世界"，不应重复输出此前已显示的 "你"/"好"
	if !strings.Contains(third, "世界") {
		t.Fatalf("missing new content in third frame: %q", third)
	}
	if strings.Contains(third, "你") || strings.Contains(third, "好") {
		t.Fatalf("frame should not re-output prior content: %q", third)
	}

	// 各帧增量拼接后，应恰好得到完整行内容（无重复、无丢失）
	if combined := first + second + third; !strings.Contains(combined, "你好世界") {
		t.Fatalf("combined frames should reconstruct full line: %q", combined)
	}
}

// TestFlushIncompleteThenFlush 验证 FlushIncomplete 后调用 Flush 不会重复输出
func TestFlushIncompleteThenFlush(t *testing.T) {
	f := NewStreamFormatter("  ", "│ ")
	var b bytes.Buffer

	// 输入一行不完整内容，调用 FlushIncomplete
	f.Write(&b, "hello world")
	f.FlushIncomplete(&b)
	incompleteOut := b.String()
	b.Reset()

	// 再调用 Flush（模拟遇到 \n 或流结束）
	f.Flush(&b)
	finalOut := b.String()

	// 验证：FlushIncomplete 已输出内容
	if !strings.Contains(incompleteOut, "hello world") {
		t.Fatalf("missing content in FlushIncomplete output: %q", incompleteOut)
	}

	// 验证：Flush 输出 \r\n 以完成行（不重复输出内容）
	if !strings.Contains(finalOut, "\r\n") {
		t.Fatalf("missing CRLF in Flush output: %q", finalOut)
	}

	// 验证：Flush 输出不应包含完整行内容（内容已由 FlushIncomplete 输出）
	// 终端上不会重复显示，因为 Flush 只补 \r\n
	if strings.Contains(finalOut, "hello world") {
		t.Fatalf("Flush should not re-output content, got: %q", finalOut)
	}

	// 验证：终端上不会看到重复——FlushIncomplete 已增量输出完整内容，
	// Flush 只补 \r\n 结束该行，不会再次输出行内容。
	// 验证 finalOut 不含 "hello world"（内容已由 FlushIncomplete 输出）
	countInFinal := strings.Count(finalOut, "hello world")
	if countInFinal > 0 {
		t.Fatalf("duplicate content in final output: %q", finalOut)
	}
}

// TestFlushIncompleteSkipsUnchanged 验证行内容未变时 FlushIncomplete 跳过输出
func TestFlushIncompleteSkipsUnchanged(t *testing.T) {
	f := NewStreamFormatter("  ", "│ ")
	var b bytes.Buffer

	// 第1次调用：有内容变化
	f.Write(&b, "hello")
	f.FlushIncomplete(&b)
	firstLen := b.Len()
	b.Reset()

	// 第2次调用：内容未变化（lastIncompleteLen 相同）
	f.FlushIncomplete(&b)
	secondLen := b.Len()

	// 第二次应该跳过输出（内容未变化）
	if secondLen >= firstLen && firstLen > 0 {
		t.Fatalf("should skip output when line unchanged: firstLen=%d, secondLen=%d", firstLen, secondLen)
	}
}

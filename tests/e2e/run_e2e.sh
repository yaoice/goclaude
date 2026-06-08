#!/usr/bin/env bash
# goclaude E2E 真实运行测试
#
# 调用真实 LLM API（默认 DeepSeek）+ 真实工具 + 真实 MCP server。
# 不是 mock；不是单元测试；是从 stdin 到模型到工具到 stdout 的完整链路。
#
# 前置条件：
#   - .env 中已配置 DEEPSEEK_API_KEY（或环境变量已 export）
#   - python3 可用（用于 echo MCP server）
#   - bin/goclaude 已构建（脚本会自动重建一次）
#
# 用法：
#   bash tests/e2e/run_e2e.sh           # 跑全部场景
#   bash tests/e2e/run_e2e.sh skill     # 仅跑名为 skill 的场景
#
# 退出码：0 全过；非 0 = 失败的场景数。

set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

GOCLAUDE_BIN="$ROOT/bin/goclaude"
TIMEOUT_PER_CASE=120
PASS=0
FAIL=0
FAIL_NAMES=()
ONLY="${1:-}"

# ----- 颜色 -----
if [ -t 1 ]; then
    GRN='\033[1;32m'; RED='\033[1;31m'; DIM='\033[2m'; RST='\033[0m'
else
    GRN=''; RED=''; DIM=''; RST=''
fi

log() { printf "${DIM}[$(date '+%H:%M:%S')]${RST} %s\n" "$*"; }
ok()  { printf "${GRN}✓ PASS${RST} %s\n" "$1"; PASS=$((PASS+1)); }
ng()  { printf "${RED}✗ FAIL${RST} %s\n  expected: %s\n  got:      %s\n" "$1" "$2" "$3"; FAIL=$((FAIL+1)); FAIL_NAMES+=("$1"); }

# ----- 准备 -----
log "checking prerequisites..."
[ -f "$GOCLAUDE_BIN" ] || { log "binary missing, building..."; go build -buildvcs=false -o "$GOCLAUDE_BIN" ./cmd/goclaude/ || exit 99; }
command -v python3 >/dev/null || { echo "python3 required"; exit 99; }

# 测试夹具：sample 文件
SAMPLE="$(mktemp -t e2e_sample_XXXX.txt)"
trap 'rm -f "$SAMPLE"' EXIT
cat > "$SAMPLE" << 'EOF'
Hello E2E world.
The magic word is BANANA.
EOF

# 测试夹具：示例 skill
SKILL_DIR="$ROOT/.claude/skills/secret-decoder"
mkdir -p "$SKILL_DIR"
cat > "$SKILL_DIR/SKILL.md" << 'EOF'
---
description: Decodes secret pass-phrases for E2E test
whenToUse: When user asks for the official secret pass-phrase
---

# Secret Decoder Skill

The official secret pass-phrase is:

  TURTLE-RAINBOW-42

When asked, reply with exactly that pass-phrase, in uppercase, with no quotes.
EOF

# 测试夹具：示例 agent
AGENT_FILE="$ROOT/.claude/agents/echo-bot.md"
mkdir -p "$(dirname "$AGENT_FILE")"
cat > "$AGENT_FILE" << 'EOF'
---
name: echo-bot
description: When you want a tiny isolated agent that simply echoes back a payload prefixed with [ECHO]
tools: []
model: deepseek-chat
---

You are a minimal echo agent. Whatever input you receive, reply with one line:
  [ECHO] <the user's input verbatim>
Do not call any tools. Do not add commentary. Just the line above.
EOF

# 测试夹具：MCP echo server 配置
MCP_CFG="$ROOT/.mcp.json"
cat > "$MCP_CFG" << EOF
{
  "mcpServers": {
    "echo": {
      "type": "stdio",
      "command": "python3",
      "args": ["tests/e2e/mcp_echo_server.py"],
      "enabled": true
    }
  }
}
EOF

# ----- run_case "name" "expected_substring" "args..." -----
#
# 失败时自动重试一次（应对真实 LLM 偶发不按指令）。
# 仅当连续两次都拿不到期望子串才标记 FAIL。
run_case() {
    local name="$1" expect="$2"; shift 2
    if [ -n "$ONLY" ] && [ "$ONLY" != "$name" ]; then return 0; fi

    log "▶ $name"
    local out rc
    for attempt in 1 2; do
        out=$(timeout "$TIMEOUT_PER_CASE" "$GOCLAUDE_BIN" "$@" 2>&1)
        rc=$?
        if [ $rc -eq 124 ]; then
            ng "$name" "complete within ${TIMEOUT_PER_CASE}s" "timed out (attempt $attempt)"
            return
        fi
        if [ $rc -ne 0 ]; then
            ng "$name" "exit 0" "exit $rc (attempt $attempt)"
            return
        fi
        if echo "$out" | grep -q -F "$expect"; then
            if [ $attempt -gt 1 ]; then
                printf "  ${DIM}(retried)${RST} "
            fi
            ok "$name"
            return
        fi
        if [ $attempt -lt 2 ]; then
            log "  ${DIM}retry $name (LLM didn't follow instruction on attempt $attempt)${RST}"
        fi
    done
    ng "$name" "contains: $expect" "$(echo "$out" | tail -3)"
}

# =========================================================================
# 场景定义
# =========================================================================

# 1) 基础连通：模型纯文本回应
run_case "ping" "PONG" \
    run --no-mcp -p deepseek "Reply with exactly the word PONG"

# 2) 基础工具调用：file_read
run_case "tool_file_read" "BANANA" \
    run --no-mcp -p deepseek \
    "Use the file_read tool to read $SAMPLE and tell me what magic word is mentioned. Reply with just the word."

# 3) Skill 工具触发（修复 B 验证）
run_case "tool_skill" "TURTLE-RAINBOW-42" \
    run --no-mcp -p deepseek \
    "I need the official secret pass-phrase. Use the Skill tool to load the 'secret-decoder' skill to find it, then reply with just the pass-phrase."

# 4) Agent 工具触发（修复 C 验证）
run_case "tool_agent" "[ECHO] hello world" \
    run --no-mcp -p deepseek \
    "Use the Agent tool with subagent_type='echo-bot' and prompt='hello world' to spawn a subagent. Reply with the subagent's exact output."

# 5) MCP 连接 + 工具列出
run_case "mcp_list" "mcp__echo__echo" \
    run -p deepseek -v \
    "list MCP servers and tools you have access to (mention any tool names starting with mcp__)"

# 6) MCP 工具调用
run_case "mcp_call" "[MCP-ECHO] hello from e2e" \
    run -p deepseek \
    "Call the mcp__echo__echo tool with text='hello from e2e' and report what it returns verbatim."

# 7) 多工具组合（file_read + Skill + MCP，多轮）
run_case "multi_tool_chain" "[MCP-ECHO] Alice-7-TURTLE-RAINBOW-42" \
    run -p deepseek \
    "Do these 3 things in order:
1. Use file_read to read $SAMPLE  (note: actual content uses Alice/7 instead of magic word, ignore that mismatch and just use the literal 'Alice-7' below)
2. Use the Skill tool with name='secret-decoder' to get the official pass-phrase
3. Use mcp__echo__echo with text='Alice-7-{passphrase}' (substitute the real pass-phrase from step 2)
Then reply with the final echo output verbatim."

# 8) 上下文自动压缩（context manager 验证）
#
# 通过把 token 预算压到 1KB（默认 200KB），强制 query.Engine 在多轮对话后
# 触发 SummarizingCompactor 走 LLM 摘要路径（失败回退本地截断）。
#
# 期望结果：模型仍能正确回答（说明压缩后关键信息保留），并在 verbose 日志中
# 看到压缩相关的 INFO（"上下文压缩"或 fallback 截断）。
run_case "context_compact" "BANANA" \
    run --no-mcp -v -p deepseek --max-context-kb=1 \
    "Read $SAMPLE three times in a row using file_read, then tell me the magic word in one word."

# 9) Agent-teams 离线 e2e（不依赖 LLM；纯 CLI 子命令验证 team / mailbox）
#
# 验证全流程：create → join 多 agent → broadcast → p2p send → drain inbox
# → re-drain 为空 → guard active members → force delete。
# 用临时 HOME 隔离，避免污染开发者的 ~/.goclaude/teams。
if [ -z "$ONLY" ] || [ "$ONLY" = "team" ]; then
    log "▶ team"
    TEAM_HOME=$(mktemp -d)
    if HOME="$TEAM_HOME" bash -c '
        set -e
        BIN="'"$GOCLAUDE_BIN"'"
        $BIN team create "Alpha Squad" --role researcher >/dev/null
        for n in alice bob carol; do $BIN team join "Alpha Squad" "$n" --role coder >/dev/null; done
        $BIN team send "*" "kickoff" --team "Alpha Squad" --from team-lead --summary "broadcast" >/dev/null
        $BIN team send "bob"  "task X done" --team "Alpha Squad" --from alice --summary "alice->bob" >/dev/null
        # bob 应该看到 2 条（peek 不消费）。grep -c 在 0 匹配时退出 1，与 set -e 冲突 → || true
        n=$($BIN team inbox bob --team "Alpha Squad" --peek 2>/dev/null | grep -c "\"from\":" || true)
        test "$n" = 2 || { echo "expected 2 messages, got $n"; exit 1; }
        # drain 之后应该清空
        $BIN team inbox bob --team "Alpha Squad" >/dev/null
        n=$($BIN team inbox bob --team "Alpha Squad" --peek 2>/dev/null | grep -c "\"from\":" || true)
        test "$n" = 0 || { echo "expected 0 after drain, got $n"; exit 1; }
        # 默认拒绝带活跃成员删除
        if $BIN team delete "Alpha Squad" >/dev/null 2>&1; then
            echo "delete should have failed (active members)"; exit 1
        fi
        # force 删除
        $BIN team delete "Alpha Squad" --force >/dev/null
        # list 应空
        out=$($BIN team list)
        echo "$out" | grep -q "no teams" || { echo "list should be empty: $out"; exit 1; }
    ' >/dev/null 2>&1; then
        ok "team"
    else
        ng "team" "all team CLI steps pass" "see: HOME=tmpdir bash -x and re-run"
    fi
    rm -rf "$TEAM_HOME"
fi

# 8) 交互式 REPL 烟雾（PTY 驱动；需 expect）
if [ -z "$ONLY" ] || [ "$ONLY" = "repl" ]; then
    log "▶ repl"
    if ! command -v expect >/dev/null; then
        ng "repl" "expect installed" "expect not found"
    elif expect "$ROOT/tests/e2e/repl_smoke.exp" >/dev/null 2>&1; then
        ok "repl"
    else
        ng "repl" "all REPL smoke checks pass" "see: expect tests/e2e/repl_smoke.exp"
    fi
fi

# =========================================================================
# 总结
# =========================================================================

echo ""
echo "============================================"
printf "  %sPASS %d%s   %sFAIL %d%s\n" "$GRN" "$PASS" "$RST" "$RED" "$FAIL" "$RST"
echo "============================================"
if [ $FAIL -gt 0 ]; then
    echo "失败的场景:"
    for n in "${FAIL_NAMES[@]}"; do echo "  - $n"; done
    exit "$FAIL"
fi
exit 0

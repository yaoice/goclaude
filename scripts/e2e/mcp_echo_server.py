#!/usr/bin/env python3
# Minimal stdio MCP server for E2E testing.
# Implements: initialize, tools/list, tools/call (single tool: echo)
# Newline-delimited JSON-RPC over stdin/stdout. Logs to stderr.

import json
import sys


def write(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()


def log(msg):
    sys.stderr.write(f"[mcp-echo] {msg}\n")
    sys.stderr.flush()


TOOLS = [
    {
        "name": "echo",
        "description": "Echo back the provided text, prefixed with [MCP-ECHO]. Useful for E2E testing the MCP integration end-to-end.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "text": {"type": "string", "description": "Text to echo back"},
            },
            "required": ["text"],
        },
    }
]


def handle(msg):
    method = msg.get("method")
    msg_id = msg.get("id")
    params = msg.get("params") or {}

    if method == "initialize":
        return {
            "jsonrpc": "2.0",
            "id": msg_id,
            "result": {
                "protocolVersion": params.get("protocolVersion", "2024-11-05"),
                "capabilities": {"tools": {"listChanged": False}},
                "serverInfo": {"name": "echo-mcp", "version": "0.1.0"},
            },
        }

    if method == "notifications/initialized":
        return None  # notifications get no response

    if method == "tools/list":
        return {"jsonrpc": "2.0", "id": msg_id, "result": {"tools": TOOLS}}

    if method == "tools/call":
        name = params.get("name", "")
        args = params.get("arguments") or {}
        if name == "echo":
            text = args.get("text", "")
            return {
                "jsonrpc": "2.0",
                "id": msg_id,
                "result": {
                    "content": [
                        {"type": "text", "text": f"[MCP-ECHO] {text}"}
                    ],
                    "isError": False,
                },
            }
        return {
            "jsonrpc": "2.0",
            "id": msg_id,
            "error": {"code": -32601, "message": f"unknown tool: {name}"},
        }

    if msg_id is not None:
        return {
            "jsonrpc": "2.0",
            "id": msg_id,
            "error": {"code": -32601, "message": f"unknown method: {method}"},
        }
    return None


def main():
    log("started")
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
        except Exception as e:
            log(f"parse error: {e}; line={line!r}")
            continue
        try:
            resp = handle(msg)
            if resp is not None:
                write(resp)
        except Exception as e:
            log(f"handle error: {e}")
            if msg.get("id") is not None:
                write({
                    "jsonrpc": "2.0",
                    "id": msg["id"],
                    "error": {"code": -32603, "message": str(e)},
                })
    log("eof, exiting")


if __name__ == "__main__":
    main()

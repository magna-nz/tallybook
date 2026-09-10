#!/usr/bin/env python3
"""Strip prompt and tool-result text from a Claude Code transcript while
keeping its structure, token counts, model ids, tool names and ids intact.

Every string longer than KEEP characters is replaced with a run of 'x' of the
same length (capped at CAP), so size-based heuristics still see realistic
numbers but no prose survives. Paths under the home directory are rewritten
to /home/user. Usage: sanitize.py in.jsonl out.jsonl
"""
import json, os, re, sys

KEEP = 48      # strings up to this length are kept verbatim (ids, names, models)
CAP = 4000     # longest replacement run
HOME = os.path.expanduser("~")
SAFE_KEYS = {"type", "role", "name", "model", "id", "tool_use_id", "uuid",
             "parentUuid", "sessionId", "requestId", "agentId", "version",
             "subagent_type", "resolvedModel", "status", "gitBranch",
             "entrypoint", "userType", "effort", "stop_reason", "timestamp",
             "promptId", "sourceToolAssistantUUID", "service_tier", "speed",
             "inference_geo", "permissionMode", "origin", "promptSource",
             "description"}

def scrub(v, key=None):
    if isinstance(v, dict):
        return {k: scrub(x, k) for k, x in v.items()}
    if isinstance(v, list):
        return [scrub(x, key) for x in v]
    if isinstance(v, str):
        v = v.replace(HOME, "/home/user")
        if key in SAFE_KEYS and len(v) <= 200:
            return v
        if key == "cwd":
            return re.sub(r"/home/user/.*", "/home/user/project", v)
        if len(v) <= KEEP:
            return v
        return "x" * min(len(v), CAP)
    return v

with open(sys.argv[1]) as src, open(sys.argv[2], "w") as dst:
    for line in src:
        line = line.strip()
        if not line:
            continue
        try:
            d = json.loads(line)
        except json.JSONDecodeError:
            dst.write(line + "\n")   # keep malformed lines so the parser is tested on them
            continue
        dst.write(json.dumps(scrub(d), separators=(",", ":")) + "\n")

#!/bin/bash
# [pine:learn-reminder]
cat >/dev/null
cat <<'PINE_HOOK_EOF'
{"systemMessage":"Pine reminder: only if you learned something durable, run 'pine learn' (this repo) or 'pine learn -g' (applies to every repo) — MEMORY.md / memory topics, not a new LRN per ticket. [pine:learn-reminder]"}
PINE_HOOK_EOF

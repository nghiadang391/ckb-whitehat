# ckb-whitehat CLI

Run an authorized whitehat security challenge against a target repo from your
terminal — the same workflow as `no-mistakes`, but for security findings.

```sh
cd cli && npm link          # or: node bin/ckb-whitehat.mjs <cmd>

ckb-whitehat init ~/src/target-repo --scope app,chain
ckb-whitehat run --target ~/src/target-repo
ckb-whitehat report --target ~/src/target-repo
```

## What it does

`run` drives a local agent CLI (`claude -p`, or `codex exec`) through the
method in `.claude/skills/ckb-whitehat/SKILL.md`:

1. triage the target repo → named attack hypotheses
2. write one deterministic PoC per hypothesis (asserting observable state)
3. run the PoCs, then the optional on-chain evidence layer
4. produce `FINDINGS.md` in the challenge workspace

The agent must print a final marker line so the CLI can report a structured
result:

```
CKB-WHITEHAT-RESULT: findings=9 fired=7 report=/abs/path/FINDINGS.md
```

## Auth — no API key, by design

We never prompt for, store, or transmit an API key. Like `no-mistakes`, the CLI
shells out to an agent binary that **already carries its own auth**:

- `claude` — uses your existing Claude Code login
- `codex` — uses its own login
- `ANTHROPIC_API_KEY` in your environment is picked up automatically by the
  agent if you prefer key-based auth; the CLI never sees it.

## Workspace layout

The challenge workspace lives **outside** the target repo, at
`~/.ckb-whitehat/<target-name>/`:

```
~/.ckb-whitehat/<target>/
├── config.json     # target path, scope, flags
├── poc/            # every PoC script (never inside the target tree)
├── FINDINGS.md     # the report
└── run.log         # agent output
```

PoCs are kept out of the target on purpose: an interpreter loads code from its
own working directory, so running a script inside an untrusted clone can
execute that clone's code.

## Commands

| Command | Purpose |
|---|---|
| `init [dir]` | create the workspace for a target (`--scope app,chain`) |
| `run [--target DIR]` | run the challenge; `--agent claude\|codex` |
| `status [--target DIR]` | show workspace state |
| `report [--target DIR]` | print FINDINGS.md |

Exit codes: `0` ok · `1` finding/error (see `run.log`) · `2` bad usage.

## Scope flags

`--scope app` — offline findings (code, DB, config).
`--scope chain` — adds the on-chain evidence layer: prove findings against
**our own** testnet addresses and local services. Never the target's live
deployment.

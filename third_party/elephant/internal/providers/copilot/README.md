# Elephant Copilot Provider

Custom Elephant provider that uses GitHub Copilot CLI to answer questions and
surface commands inside Walker.

## Features

- Q&A via `copilot -p` (standalone) or `gh copilot -- -p` (wrapper)
- Model selection (configurable list)
- Command extraction (code fences + `$` / `>` prompt lines)
- Copy answer and copy command actions
- Copy all extracted commands
- Open new terminal with a prefilled command (not executed)
- Temporary and persistent sessions

## Build

```bash
go build -buildmode=plugin -o build/copilot.so
```

## Install

```bash
mkdir -p ~/.config/elephant/providers
cp build/copilot.so ~/.config/elephant/providers/
```

## Configuration

Create `~/.config/elephant/copilot.toml`:

```toml
enabled = true
cli_mode = "both" # auto|copilot|gh|both
default_model = "claude-sonnet-4.5"
models = [
  "claude-sonnet-4.5",
  "gpt-5.2-codex",
  "gpt-4.1",
]

# CLI args
copilot_args = ["--no-color", "--silent"]
gh_copilot_args = ["--no-color", "--silent"]

# Session storage (defaults to XDG_STATE_HOME/elephant/copilot)
# session_dir = "/home/user/.local/state/elephant/copilot"

# Clipboard and terminal integration
clipboard_cmd = "wl-copy"
terminal_prefill_cmd = "bash -lc 'read -e -i %CMD% -p \">>> \" cmd; exec $SHELL'"

# Optional command extraction override
# command_extract_regex = "(?m)^\\$\\s+(.+)$"
```

## Walker Integration

Add `copilot` to Walker providers (installed + prefixed, but not default).
See the dotfiles integration scripts for the exact config changes.

## Notes

- The provider is opt-in: `enabled` defaults to `false`.
- The `terminal_prefill_cmd` uses `%CMD%` as a placeholder and is shell-escaped.

### Elephant Niri Sessions

Create predefined session layouts and open them.

#### Features

- run custom commands to open windows
- position windows according to definition

#### Requirements

- `niri`

#### Example Sessions

```toml
[[sessions]]
name = "Work"

[[sessions.workspaces]]
windows = [
  { command = "uwsm-app -- footclient", app_id = "footclient" },
  { command = "uwsm-app -- firefox-developer-edition", app_id = "firefox-developer-edition" },
]

[[sessions.workspaces]]
windows = [
  { command = "uwsm-app -- teams-for-linux", app_id = "teams-for-linux" },
  { command = "uwsm-app -- discord", app_id = "discord" },
]

[[sessions.workspaces]]
windows = [{ command = "uwsm-app -- tidal-hifi", app_id = "tidal-hifi" }]

[[sessions]]
name = "Private"

[[sessions.workspaces]]
windows = [
  { command = "uwsm-app -- firefox-developer-edition", app_id = "firefox-developer-edition" },
  { command = "uwsm-app -- discord", app_id = "discord" },
]

[[sessions.workspaces]]
windows = [{ command = "uwsm-app -- tidal-hifi", app_id = "tidal-hifi" }]

[[sessions]]
name = "Walker"

[[sessions.workspaces]]
windows = [
  { command = "uwsm-app -- footclient -D /home/andrej/Documents/walker -e nvim", app_id = "footclient" },
  { command = "uwsm-app -- footclient -D /home/andrej/Documents/walker", app_id = "footclient" },
]

[[sessions]]
name = "Elephant"

[[sessions.workspaces]]
windows = [
  { command = "uwsm-app -- footclient -D /home/andrej/Documents/elephant -e nvim", app_id = "footclient", after = [
    "niri msg action focus-window --id %ID%",
    "niri msg action fullscreen-window",
  ] },
  { command = "uwsm-app -- footclient -D /home/andrej/Documents/elephant", app_id = "footclient" },
]
```

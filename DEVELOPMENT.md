# Developing Muxer

Muxer is a Go relay in `relay/` and an Android app in `app/`. The [README](README.md) covers installing and using it.

## Building and testing

```sh
# relay
cd relay && go test -race ./... && go build ./cmd/muxer

# Android app (JDK 21 and the Android SDK)
./gradlew :app:testDebugUnitTest :app:assembleDebug
```

To run the relay next to an installed copy, give it its own state directory and port:

```sh
cd relay && MUXER_STATE_DIR=/tmp/relay-dev go run ./cmd/muxer serve --listen 127.0.0.1:18765
```

On the Android emulator the computer is `http://10.0.2.2:18765`. Debug builds allow plain HTTP to that address only.

### End-to-end tests against cmux

`relay/e2e` builds the relay, pairs a test phone and drives every feature against a running cmux: workspaces, groups, checklists, terminal input and history, notifications, keep awake, metrics, and agent approvals. Approvals are raised through `cmux hooks feed`, so no agent runs. The tests only touch workspaces named `e2e-…` and close them afterwards. The automations test swaps `~/.cmuxterm/automations.json` for its own rules that never fire, then puts the original back byte for byte (or removes the file if there was none) and reloads cmux. Your own rules are off while it runs.

```sh
cd relay && go test -tags e2e -count=1 ./e2e/
```

Set `E2E_SNAPSHOT_DIR` to save cmux's raw answers for comparison. The E2E workflow runs the suite every night on a macOS runner against the latest cmux and opens an issue when it fails.

The suite's relay talks to cmux through a recorder. One test builds a demo scenario (the "Payments API" and "Docs site" workspaces, terminal output, a checklist, a notification and a pending approval) and records what cmux answers. It then compares the shape of those answers with `relay/e2e/testdata/recording.json` and fails when cmux removed a field or changed its type. New fields are only logged. If cmux changed on purpose, update the relay and the app, then commit the new `recording.json` from the run's `cmux-e2e` artifact. Only record in CI: a local recording contains your own workspaces and notifications.

### App UI tests

`app/src/androidTest` drives the whole app on an emulator against the real relay, with `cmux-replay` serving the committed recording in place of cmux. The tests look for the scenario's names, so keep `RecordedCmuxScenario` in step with `relay/e2e/app_scenario_test.go`. With an emulator running:

```sh
scripts/app-ui-tests.sh
```

The App UI workflow runs them on every push and pull request. `cmux-replay` can also be started by hand; its control port takes `POST /emit?name=<event>` to send a recorded event and `GET /calls?method=<method>` to list what the relay asked.

## Releasing

Publish a GitHub release whose tag is a semantic version such as `0.2.0` or `0.2.0-beta.1`, with no `v` prefix. The release workflow tests everything, then attaches the relay for Apple silicon and Intel Macs, the signed APK, and `SHA256SUMS` to that release. The app's version name is the tag, and its version code is `major × 1,000,000 + minor × 1,000 + patch`.

Signing uses the `SIGNING_KEYSTORE_BASE64`, `SIGNING_KEYSTORE_PASSWORD` and `SIGNING_KEY_PASSWORD` secrets and the `SIGNING_KEY_ALIAS` variable.

For releases that are not marked as pre-releases, the workflow also updates the `muxer` formula in [gantoine/homebrew-tap](https://github.com/gantoine/homebrew-tap). It renders the formula with `packaging/homebrew/render-formula.sh` and pushes as a GitHub App that is installed only on the tap with contents write access. The workflow reads the app's ID from the `HOMEBREW_TAP_APP_ID` variable and its private key from the `HOMEBREW_TAP_APP_PRIVATE_KEY` secret, and each token it creates lasts one hour.

After the files are attached, a second job uploads the APK and both relay tarballs to [VirusTotal](https://www.virustotal.com) with `packaging/virustotal/scan.sh`. It waits for the scans to finish and adds a table of detection counts and report links to the end of the release notes. Rerunning the job replaces that table instead of adding a second one. The job reads its API key from the `VT_API_KEY` secret, and a free key is enough because the script waits between requests to stay under the public API rate limit.

## Relay protocol

The phone opens `GET /v1/ws` with `Authorization: Bearer <token>` and exchanges JSON messages.

| From phone | Purpose |
| --- | --- |
| `hello {notifications_after}` | Start the session. The answer carries the computer name, the cmux version and `unsupported`, the request types this relay understands but can't serve with this cmux (always a list, empty when everything works). Those requests answer with the error code `unsupported`. |
| `attach {workspace_id, surface_id, fit?}` / `detach` / `resync` | Choose the terminal to stream. Attaching also marks that terminal's notifications read on the Mac. Each `screen` frame carries `history_rows`, the number of lines above the screen, so the phone knows when to fetch `history` again. |
| `input {text}` / `key {key}` / `paste {text, submit}` / `scroll {lines}` | Send input to the attached terminal. |
| `history {lines?}` | Get up to 5,000 lines above the attached terminal's screen, oldest first, with their own style table and `total`, the number of lines above the screen when they were read. The default is 2,000. |
| `fit {columns, rows}` / `unfit` | Ask cmux to size the terminal for the phone. |
| `create_workspace`, `rename_workspace`, `close_workspace`, `create_terminal` | Manage workspaces. |
| `close_terminal {workspace_id, surface_id, force?}` / `rename_terminal {workspace_id, surface_id, title}` / `restart_terminal {workspace_id, surface_id}` | Close, rename (an empty title clears the name) or restart one terminal. Closing a workspace's only terminal fails with `last_terminal`. |
| `refresh` | Send the workspace list now instead of at the next poll. |
| `answer_approval {approval_id, mode?, answers?, feedback?}` | Answer an agent's permission request, question or plan. |
| `send_agent_message {text}` | Send up to 32 KB to the agent in the attached terminal as a cmux agent message. |
| `keep_awake` / `set_keep_awake {enabled}` | Read or change whether cmux keeps the computer awake. |
| `host_usage` | CPU and memory use of the whole computer: `{cpu_percent, memory_used_bytes, memory_total_bytes}`. |
| `vms` / `set_vm_paused {vm_id, paused}` | List the cmux Cloud VMs with their status and, for running ones, their stats, or pause or resume one. The answer to `set_vm_paused` is the VM's new state. Both fail with `cloud_disabled` when cmux Cloud is off. |
| `top` | CPU and memory in cmux: totals, each coding agent, and each workspace with its five heaviest processes. The app asks every 3 seconds while its Metrics page is open. |
| `notifications` | List the notifications in cmux's notification center, read ones included, with `is_read`. |
| `mark_notifications_read {notification_id? \| surface_id? \| all?}` | Mark one notification, one terminal's notifications or all of them read on the Mac. |
| `send_images {images, text?}` | Paste 1 to 4 images (`{data_b64, format}`, up to 5 MB each) and optional text into the Claude or Codex session in the attached terminal, then submit. |
| `recent_sessions {query?, limit?}` | List past agent sessions from cmux's Vault, newest first, or search their transcripts. A session running in a terminal comes with that terminal's `workspace_id` and `surface_id`. The default limit is 50 and the most is 200. |
| `resume_session {agent, session_id}` | Open a session that is already running, or open a workspace in the session's folder and run its resume command there. The answer is `{workspace_id, surface_id, already_open}`. |
| `installed_agents` | List the agents a task can start, `claude` and `codex`, that the user's interactive login shell finds on its `PATH`: `{agents}`. When the shell fails, both are listed. |
| `start_agent_task {agent, cwd, prompt, title?}` | Open a workspace in `cwd` (an absolute path or one starting with `~/`) with the prompt, up to 32 KB, in its `MUXER_TASK_PROMPT` environment variable, then run `claude -- "$MUXER_TASK_PROMPT"` or `codex -- "$MUXER_TASK_PROMPT"` there, so the prompt never goes through shell quoting. The answer is `{workspace_id, surface_id}`. Fails with `not_found` when the folder doesn't exist and `agent_missing` when the agent isn't installed. |
| `workspace_action {workspace_id, action, color?, description?}` | Pin or unpin a workspace, mark it read or unread, set or clear its color, or set its description (an empty one clears it). |
| `set_workspace_status {workspace_id, status}` | Put a workspace in a lane: `todo`, `working`, `needs-attention`, `review` or `done`, or `auto` to let cmux choose. |
| `checklist`, `add_checklist_item`, `edit_checklist_item`, `set_checklist_item_state`, `remove_checklist_item`, `clear_checklist` | Read or change a workspace's checklist. Each takes `workspace_id`, plus `item_id`, `text` or `state` (`pending`, `in-progress`, `completed`) as needed, and answers with the whole checklist. `clear_checklist` removes every item. |
| `create_group {name, workspace_ids}`, `rename_group`, `pin_group {pinned}`, `set_group_color {color}`, `add_to_group`, `remove_from_group`, `ungroup` | Manage workspace groups by `group_id` and `workspace_id`. `ungroup` keeps the workspaces. Removing a group's anchor workspace dissolves the group. |
| `create_workspace_in_group {group_id, title?}` | Open a workspace in a group. The answer is `{workspace_id, surface_id}`. |
| `reconnect_workspace {workspace_id}` | Reconnect an SSH workspace. |
| `automations` | List the rules in `~/.cmuxterm/automations.json`. Each has `id`, `enabled`, `trigger` (the event or category in words), `conditions`, `actions` (`{type, summary, fields}`), `rate_limit` and, when it fired since cmux started, `last_run`. When cmux couldn't read the file, the list is empty and `config_error` says why. |
| `set_automation_enabled {rule_id, enabled}` | Turn a rule on or off. cmux writes the change to the file. The answer is the same as `automations`, once cmux has applied it. |
| `automation_logs {rule_id?, limit?}` | cmux's recent automation log lines (firings, skips, errors and config changes), newest first, for one rule or all. The default limit is 100 and the most is 256. cmux keeps these in memory only. |
| `reload_automations` | Make cmux read the file again, then answer like `automations`. |
| `port_link {port}` | Find the tailnet address of a port a terminal listens on: `{port, url, exposed, needs_expose}`. A port open on every interface (or the tailnet address) is `http://<tailnet name>:<port>`. A loopback-only port answers `needs_expose: true` with the `https://` address it would get. Only a terminal's own ports are allowed, never the relay's. They are the ports cmux reports, or, when it reports none, the ports `lsof` finds for processes that `system.top` places in that terminal (or their descendants). The workspace list's `listening_ports` uses the same data. Fails with `tailscale_missing`, `tailscale_offline`, `port_not_allowed`, `not_listening` or `serve_conflict`. |
| `expose_port {port}` | Run `tailscale serve --bg --https=<port>` in front of a loopback-only port and answer like `port_link`. Ports that are reachable already are left alone. The relay records what it exposed in `exposed-ports.json` in its state folder. |
| `unexpose_port {port}` / `exposed_ports` | Stop serving a port Muxer exposed, or list them, as `{ports: [{port, url}]}`. Serve entries Muxer didn't create, or that changed since, are never removed and drop off the list. |

# phcloud

A small terminal manager for PostHog Cloud runs, made to live in the [cmux](https://cmux.com) Dock.

- **List** your runs: what's working, what needs you, what's done. Search all of them.
- **Start** a run: pick a repository, Claude or Codex, and who pays for Claude.
- **Answer** approvals, **stop** a run or **archive** it without leaving the list.
- **Open** a run as a cmux workspace, grouped by repository, with a live view of the agent and a prompt to reply.

While the manager is open, it also:

- sets each run's workspace status (working, needs attention, review, done);
- sends a cmux notification when a run needs you or finishes;
- hands your Claude token to runs you started on your own Claude plan.

phcloud talks to PostHog and cmux directly. It needs no server, browser page or relay.

## Install

```sh
go install github.com/gantoine/muxer/phcloud/cmd/phcloud@latest
phcloud connect      # paste a personal API key
phcloud dock install # pin the manager in the cmux Dock
```

The personal API key needs the `task:read`, `task:write`, `user:read` and `integration:read` scopes. Pass `--host https://eu.posthog.com` to `connect` on the EU cloud.

To bill Claude to your own plan, run `claude setup-token`, then `phcloud claude-token` and paste the token.

## Keys

| Key | In the list |
| --- | --- |
| `↑` `↓` / `j` `k` | move |
| `enter` | open the run's workspace |
| `n` | start a run |
| `a` / `A` / `d` | allow once / always / deny the waiting approval |
| `s` | stop the agent's turn |
| `x` | archive (asks first) |
| `w` | open in PostHog |
| `/` | search all runs |
| `tab` | switch between Active and All |
| `T` | save a Claude token |

In a run's workspace you get the conversation with markdown, tool calls folded into one row, and a composer at the bottom:

| Key | In a run |
| --- | --- |
| `enter` / `alt+enter` | send / new line |
| `1` `2` `3`… | pick an answer when the agent asks |
| `esc` | stop the agent's turn |
| `ctrl+o` | show or hide tool details |
| `pgup` `pgdn`, wheel | scroll |
| `ctrl+c` | close the view; the run keeps going |

New runs open on the prompt and remember the last repository, agent and billing. Press `tab` to change them.

## Commands

```
phcloud                  the manager
phcloud connect | status | disconnect
phcloud claude-token [--clear]
phcloud new --repo owner/name [--agent claude|codex] [--claude-plan] "prompt"
phcloud attach TASK RUN
phcloud dock install | uninstall
```

Settings live in `~/.config/phcloud` (the key is stored with `0600` permissions). Set `PHCLOUD_CONFIG_DIR` to use another folder.

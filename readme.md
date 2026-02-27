__Deckard__: terminal command center for managing parallel Claude Code sessions via Git worktrees.

## About
Deckard is a worktree-native session manager for AI coding workflows.

It gives you a split-pane terminal UI showing:
- all active worktrees
- linked Claude sessions
- MR + CI status
- which tasks need human input right now

From one place, you can resume sessions, open MRs, monitor pipelines, and retire
work when it’s merged — without juggling terminal tabs or losing track of
parallel work.

Deckard turns your repo into an operations console for AI-assisted development.

## Installation

```
make install
```

Installs the `deckard` binary to `~/.local/bin`. Make sure that’s on your `$PATH`.

## Developing Deckard

Deckard is self-hosting — you use Deckard to work on Deckard. Because restarting
the running tool is inherently self-referential, the dev workflow keeps a separate
terminal alongside Deckard rather than trying to hot-reload in place.

**Setup:**

1. Open a spare terminal (outside Deckard)
2. `cd` into the worktree you’re working on
3. Run `make dev` — this starts `air`, which rebuilds the binary automatically whenever you save a `.go` file

**Verify a change:**

1. Save your `.go` file — air rebuilds into `tmp/deckard` within a second or two
2. In the spare terminal you’ll see `running...` when the build succeeds (or an error)
3. Run `make install` to push the new binary to `~/.local/bin`
4. `ctrl+]` to detach from any attached session back to the Deckard dashboard
5. `q` to quit Deckard
6. `deckard` to relaunch with the new binary

**Why not auto-reload?**

With multiple worktrees in flight, several `air` instances could rebuild conflicting
versions of the binary simultaneously. A clean manual relaunch is safer and takes
about five seconds.

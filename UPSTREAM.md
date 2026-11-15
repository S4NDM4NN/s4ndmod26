# Upstream projects and history

S4NDMoD26 is built on the work of other projects. This file records where
the code came from, and how to read the history that came with it.

## iortcw (engine, `iortcw/`)

- Project: https://github.com/iortcw/iortcw (maintained by Zack Middleton,
  MAN-AT-ARMS, Donny Springer and many contributors)
- Imported from: `master` at `438e7d41` (2024-05-26)
- Our `iortcw/` is the upstream `MP/` tree plus its top-level files; the
  single-player `SP/` tree is not used here.
- Upstream history is included in this repository. Commit hashes differ
  from upstream because paths were rewritten (`MP/*` and root files to
  `iortcw/*`, `SP/` dropped). Authors, dates and messages are unchanged.
  To find an upstream commit, search the message or date.
- `git log -- iortcw/<file>` and `git blame iortcw/<file>` show the real
  upstream authors for lines we have not changed. The earliest upstream
  commit shows as a boundary (`^`) in blame.

## Omni-bot (`omnibot/`)

- Project: https://github.com/jswigart/omni-bot
- Based on branch `stable` at `b3f10f33`. Its history is already in this
  repository, under the older paths `0.83/Omnibot`, `bot/omnibot` and
  `omnibot`. Use `git log --follow` to cross the renames.

## Licences

iortcw keeps its own licence: see `iortcw/COPYING.txt` and
`iortcw/LICENCE.md`. Omni-bot has no licence file at its root; third-party
libraries bundled with it carry their own notices in their source trees.

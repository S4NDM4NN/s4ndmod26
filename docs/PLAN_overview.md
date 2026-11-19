# s4ndmod26 plan overview (for review)

Written 2026-10-16. This is the short version. Detail and sources are in `PLAN_osp_stats.md` and
`PLAN_launcher_accounts.md`.

How to read this: the plan has two stages. **Stage 1 is OSP parity, especially stats.** Accounts, saved
stats and the website come **after** Stage 1 is settled. Items marked **[DECIDE]** need your answer.

---

## 1. Order of work

| Stage | What | Depends on |
|---|---|---|
| 1 | OSP-style stats in the mod: data, `stats`/`statsall`/`scores`, stats file, overlays, scoreboard and font polish | nothing |
| 2 | Backend: Postgres, ingest of the stats file, faster games list, replay retention | Stage 1 stats record |
| 3 | Accounts: email login, join tickets, "my games", stats pages, weekly summary | Stage 2 |
| 4 | Replay viewer (seek, slow-mo, freecam, jump to player) | can run alongside 2-3 |

Why stats first: every later stage consumes the stats record, so its contents should be right before anything
stores it. Games played before stats and identity are recorded can't be recovered.

---

## 2. Stage 1: OSP features checklist

Legend for "OSP": **doc** = in an OSP document, **dump** = seen in a real OSP stats dump,
**shot** = seen in your screenshots. "Ours" is what the repo has today.

### 2.1 Stats commands
| Feature | OSP | Ours | Proposal |
|---|---|---|---|
| `stats` (own accuracy block) | doc, dump | simple self-only summary (`Cmd_Stats_f`) | replace with the OSP layout |
| `stats <player_ID>` | doc | no | add |
| `statsall` | doc | no | add |
| `scores` (team tables + totals) | doc, dump | **end-of-round only** (`RoundStats()`, rows match OSP; header `GB` vs `Gib`) | rename `GB` to `Gib`; also callable on demand as `scores` |
| `statsdump` to a `.txt` | doc | no | add; same text as `statsall`+`scores` |
| `topshots` / `bottomshots [weapon]` | doc | no | add |
| Auto stats file at round end (`cg_autoAction` bit 4) | doc | not checked | [DECIDE] a server-side always-on file instead of a client option? |

### 2.2 What gets counted
Already in the mod (`clientPersistant_t`): kills, deaths, team kills, suicides, gibs, team gibs, revives, packs
picked up, shots, hits, headshots, damage given/received, team damage, goombas, poisons, knife kills.

| Stat | OSP definition | Ours | Needed work |
|---|---|---|---|
| Per-weapon attempts / hits | doc, dump | total only | new per-weapon counters |
| Per-weapon kills | dump | no | count by killer's weapon |
| Per-weapon deaths (died to that weapon) | dump | no | count by weapon that killed you |
| Per-weapon headshots | dump | total only | per weapon; bullet weapons only |
| Syringe row (hits = revives) | doc, dump | revives only | add attempts |
| Health/ammo packs: hits = used, attempts = dropped (they are weapon rows) | doc, dump | "used" only | add "dropped" counter |
| Revivals | doc, dump | yes | none |
| Eff = kills / (kills + deaths), truncated | doc, dump | no | derived when printing |
| Gib (bodies gibbed) | doc | yes | none |
| Damage given / received / team damage | dump | yes | none |
| Score | dump | yes | store the final score |
| Rounds ("1 Round", "0 Rounds") | dump, shot | no | per-round tracking and reset |
| Objective events (plant, defuse, repair, destroy, with bonus) and pack score events | doc (logged as events) | not checked | events only, as OSP did (see decisions) |

Rules OSP documents that we should copy:
- No shot attempts counted when gibbing bodies.
- `\kill` counts as a death.
- Stats reset correctly between rounds (OSP had bugs here).
- A weapon row appears if it was used **or** you died to it; accuracy is blank when attempts are 0.

### 2.3 In-game overlays and scoreboard
| Feature | OSP | Ours | Proposal |
|---|---|---|---|
| `+wstats` window (weapon table, bottom left) | doc, shot | no | add; scale cvar `cf_wstats` (1.2) |
| `+wtopshots` window (bottom right) | doc, shot | no | add; scale cvar `cf_wtopshots` (0.8) |
| Spectating: show the followed player's stats | doc | no | add |
| Overlays stay on top of the scoreboard | shot | no | draw after the scoreboard |
| Date/time on the scoreboard | doc, shot | not checked | add if missing |
| `Avg Ping` per team | doc, shot | no (not in `cg_scoreboard.c`) | add |
| Ready marker `?` / `!` in warmup | doc, shot | not checked | [DECIDE] needs a warmup-ready concept; skip for bots-first play? |
| Spectator ping | doc | not checked | low priority |

### 2.4 Look and feel
| Feature | Notes | Proposal |
|---|---|---|
| OSP fonts | Your screenshot looks the same as ours. The difference is that OSP switches fonts live. | make `cg_ospFonts` take effect without a restart; consider an OSP-style `ch_font 0\|1\|2` |
| ET fonts (Arial Black / Courier Bold TrueType, used by ET's stats windows) | you find them nicer than OSP's | [DECIDE] bake free look-alike fonts for the stats windows; do not copy ET's font files (EULA). See `PLAN_osp_stats.md`. |
| Weapon icons look crisper in OSP | cause unknown | compare side by side, check texture size, shader flags, filtering, scaling |
| 32 colour codes (`^8`, `^A`-`^O`...) | doc | [DECIDE] worth having? |

### 2.5 Not copying (for now)
Ready/pause/referee/lock/specinvite, voting extras, ignore, democast (MP3 shoutcast), and server config presets.
They are competitive or admin features and don't fit bots-first play. Say if you want any back.

---

## 3. The stats record (the thing to approve)

One record per match per human player, written at intermission **and** on disconnect or map change. It feeds:
1. the in-game text (`stats`, `statsall`, `scores`, `statsdump`),
2. the overlays,
3. later, the backend.

Contents:
- Identity placeholder (slot, name; account ID later), `is_bot` flag, team, class.
- Totals: kills, deaths, suicides, team kills, gibs, damage given/received, team damage, revives, score, rounds.
- Weapon rows, OSP's five values per weapon: `{weapon, hits, attempts, kills, deaths, headshots}`. Packs and the
  syringe are rows too (packs: hits = used, attempts = dropped; syringe: hits = revives).
- Objective events (later).
- Match key: the same name as the replay file, so a stats record and its replay line up.

Test plan: your two real OSP dumps become golden-output tests for the text printer. Strict checks:
per-weapon kills sum to kills, and per-weapon deaths sum to deaths (when suicides are 0).
We will **not** assert that one team's damage given equals the other's damage received; one dump matches
and the other doesn't.

---

## 4. Later stages (summary only)

- **Backend:** Postgres plus the existing Go service. No Kafka. Redis only if it already exists.
  Stats come from the mod's stats file, not from replay parsing.
- **Retention:** full replays 24-48h; POTG and highlight clips trimmed and kept longer; stats kept forever.
  Saved replays break across versions, so embed version fields. A rendered MP4 is the only durable clip.
- **Accounts:** email -> code or link -> revocable device token; the game server checks a short-lived signed ticket
  and records an account ID. No Authentik (admin-only). Browser and launcher auto-login.
- **Privacy:** no social features, no IPs, stats private by default, account deletion removes data.
- **Pages:** "my games", stats and breakdowns, topshots leaderboards, opt-in weekly summary email.
- **Viewer:** modeled on OSP's demo controls: timescale with readout, controls window, scoreboard, zoom, capture
  presets; seeking backward needs periodic keyframes (new replay format).

Out of scope: temp or self-hosted servers, friends, MMR.

---

## 5. Decisions and open items

**[DECIDE]**
- [x] World deaths: a "World" pseudo-weapon row, deaths only (decided). Each death is counted in exactly one row; `Sui` is a subset tally; world deaths count in `Dth`.
- [x] Weapon slots: only weapons usable in multiplayer (decided). Draft mapping in `PLAN_osp_stats.md`; please review it.
- [ ] Objective stats: OSP logged events only (plant, defuse, repair, destroy, revive, pack). Keep events only?
- [ ] Always write a stats file at round end on the server, or leave it as a client option like OSP?
- [ ] Warmup ready markers: build, or skip?
- [ ] Extra colour codes: build, or skip?
- [ ] Assists and a kill matrix: add beyond OSP parity, or leave for later?
- [ ] Topshots minimum shots (OSP's value is unknown): pick a default.

**Still unknown**
- [x] `osp-wolf-StatsInfo.txt` - received; definitions are in `PLAN_osp_stats.md`.
- [ ] `osp-wolf-Multiview.txt`, and the OSP server readme (only needed for the viewer and server settings).
- [ ] What `ch_font` was set to in your OSP screenshot.
- [ ] A capture of our scoreboard and weapon icons at 1280x1024 for side-by-side comparison.
- [ ] Why OSP's weapon icons look crisper.

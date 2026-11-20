# Plan: OSP-style stats, stats overlays, and the data pipeline behind them

Status: planning notes (not committed). Written 2026-10-16. Companion to `PLAN_launcher_accounts.md`.

Labels used below: **[doc]** = stated in an OSP document, **[dump]** = read from a real OSP stats dump,
**[inferred]** = deduced from numbers or code, not confirmed, **[mod]** = verified in this repo.

## Goals
1. Reproduce OSP's stats in s4ndmod26: the `stats` / `statsall` / `scores` output, `statsdump` to a file,
   and the `+wstats` / `+wtopshots` HUD overlays.
2. Make the server the authoritative source of stats, so the website never has to derive them from replays.
3. Use the same stats record for the in-game printer, the intermission file, and the website backend
   (accounts, "my games", breakdowns, weekly summaries).
4. Longer term: a full replay viewer with modern demo-viewer features.

Out of scope for now: temp or self-hosted servers, friends or social features, MMR (games are against bots),
competitive features (ready/pause/referee/lock/voting).

## Decisions so far
- **Stats come from the game server**, not from `.rpl` files. Replays are only for playback.
- **Full replays live 24-48h.** POTG and highlight clips are trimmed and kept longer. Players can save replays
  locally; saved replays break when the format changes, so version fields must be embedded. A rendered MP4 is
  the only durable form for long-kept clips.
- **Accounts:** email -> one-time code/link -> revocable device token. No Authentik (admin-only). The server
  validates a short-lived signed join ticket and records an account ID per player.
- **Postgres + the Go backend** (status-api). No Kafka. Redis optional, only if it already exists.
- **Privacy:** stats are private to the account by default, no IPs stored, account deletion removes data.

## What OSP does (sources)
Docs: OSP for Wolfenstein client readme, command reference, history (v0.9, Dec 2003/Jan 2004), demo controls,
democast, and StatsInfo (now received, see below). `osp-wolf-Multiview.txt` has **not** been seen yet.

### Commands [doc]
- `stats [player_ID]` - weapon accuracy block for one player. `statsall` - for all players.
- `scores` - current match stat info (the team tables).
- `statsdump` - saves weapon/game stats to a `.txt` in `stats/`. `cg_autoAction` bit 4 does this at round end.
- `topshots [weapon_id]` / `bottomshots [weapon_id]` - best/worst accuracies, optionally for one weapon.
- `+wstats` - HUD overlay of weapon stats (or of the followed player when spectating).
  `+wtopshots` - HUD overlay of the best accuracies.
- Client settings: `cf_wstats` (font scale of the +wstats window, default 1.2), `cf_wstopshots` (default 0.8),
  `cg_autoAction` bitmask (1 demo at round start, 2 screenshot at round end, 4 stats file at round end),
  `ch_font 0|1|2` (0 original RtCW font, 1 and 2 alternate fonts).

### Stats semantics [doc, history]
- Shot attempts are not counted when gibbing bodies (0.82).
- `\kill` counts as a death (0.9).
- "GS" renamed "Gib": number of bodies gibbed; also shown in +wstats (0.9).
- Health pack and ammo pack **drop/used** stats; topshots has a 15-drop minimum (0.9).
  So `Healthpacks: 30/42` means 30 used out of 42 dropped. Ammo packs can't be picked up when all banks are full.
- Revival stats: number of times a player revived a teammate (0.9).
- Stats must be wiped between rounds correctly (0.82 fixed "carry-over of stats when they should be wiped").
- Server logging added for objectives (0.8): `Dynamite_Plant`, `Dynamite_Diffuse`, `Repair`,
  `Objective_Destroy <player> <bonus>`, `Medic_Revive <medic> <revived>`, `Ammo_Pack <from> <to>`,
  `Health_Pack <from> <to>`. OSP treated these as events, not per-player counters.

### Output format [dump]
Personal block (`stats`):
```
Accuracy info for: S4NDM4NN (1 Round)

Weapon     Acrcy Hits/Atts Kll Dth HS
-------------------------------------
Luger    :  46.2    6/13     1   0  0
Colt     :                   0   1  0
MP-40    :  31.1   55/177    1   1  1
Thompson :                   0   6  0
Sten     :                   0   1  0
Panzer   :                   0   2  0
Syringe  :  75.0    6/8      0   0

Healthpacks: 30/42
Revivals   : 6

Damage Given: 914     Bodies Gibbed: 0
Damage Recvd: 2569    Team Damage  : 0
```
Team table (`scores`, one per team, plus a totals row):
```
TEAM   Player          Kll Dth Sui TK Eff Gib    DG    DR   TD  Score
---------------------------------------------------------------------
Axis   murkey           11  13   2  0  45   3  2440  2783   32     40
...
Axis   Totals           64  81   9  3  44  13 14370 16890  632    117
```
Footer: `Stats recorded: 23:16:33 (03 Jan 2019)`.

Observations from two real dumps:
- **Eff = kills / (kills + deaths), truncated** [inferred, matches every row]. Score is sorted descending.
- Names are colour-stripped and cut at 15 characters; spaces are kept (`E T E R N A L`).
- A weapon appears if it was used or if the player died to it. Accuracy columns are blank when attempts are 0.
  HS is blank for non-bullet weapons (Syringe, Airstrike).
- **Per-weapon Dth = deaths to that weapon** (victim side): the weapon deaths sum to the player's Dth,
  and weapon Kll sums to Kll [inferred; holds in both dumps for players with 0 suicides].
- **Syringe is a weapon row**: hits are revives, so `Revivals` = syringe hits [inferred; 6 = 6/8].
- The personal block adapts to class: medic shows Healthpacks + Revivals, Lieutenant shows Ammopacks.
- Score is not derivable from kills (a player with 0 kills scored 30), so store the server's score.
- **Cross-team totals do not reconcile**: in one dump Axis DG equals Allies DR, in the other it does not
  (14370 vs 13306). Likely players who left, team and self damage. Do not assert it in tests.
  Players who leave mid-game are probably missing from OSP's tables, so **record stats at disconnect**.

## Authoritative definitions: `osp-wolf-StatsInfo.txt` (received)
Header says v0.9, 30 Dec 02, but it lists pack stats that the history only credits to 0.9 (Jan 2004), so treat the
file dates as unreliable; where the history disagrees (e.g. `\kill`), the later history entry wins.

Team table columns [doc]:
- `Kll` enemies killed. `Dth` deaths **caused by enemies**. `Sui` suicides. `TK` team kills.
- `Eff` = Kll / (Kll + Dth). `Gib` = gibs made (forcing an enemy into unrevivable limbo).
- `DG` damage given, `DR` damage received, `TD` damage inflicted on teammates. Team totals are plain sums.

Server log lines [doc] (the model for our stats record):
```
Dynamite_Diffuse: <player_id>          Dynamite_Plant: <player_id>        Repair: <player_id>
Medic_Revive: <medic_id> <revived_id>  Objective_Destroy: <player_id> <bonus>
Ammo_Pack: <from> <to>                 Ammo_Pack_Score: <from>   (scoring event)
Health_Pack: <from> <to>               Health_Pack_Score: <from> (scoring event)
WeaponStats: <player_id> <round_#> <weaponmask> [<weapon info> ...] <dmg_given> <dmg_rcvd> <team_dmg> <gibs>
```
- One `WeaponStats` line per player **per round**. `round_#` is total rounds played.
- `weaponmask` marks which weapons have an entry; only tracked weapons appear, in low-bit-first order.
- Each weapon entry is five values: `<hits> <attempts> <kills> <deaths> <headshots>`.
- The 19 weapon slots (bit): Knife 0, Luger 1, Colt 2, MP40 3, Thompson 4, Sten 5, Sniper Rifle 6, CrewGun 7,
  Panzerfaust 8, Venom 9, Flamethrower 10, **Health Packs 11**, Grenade 12, Mortar 13, Dynamite 14, Airstrike 15,
  Artillery 16, **Syringe 17**, **Ammo Packs 18**.
- So packs and the syringe are ordinary weapon rows: for packs hits = used and attempts = dropped
  (`30/42`), for the syringe hits = revives. Confirms the earlier inference.

Consistent with the real dumps [inferred]: deaths exceed the opposing team's kills in both dumps (33 vs 27; 81 vs 71
and 81 vs 64). That fits players who left being dropped from the tables, which is another reason to record stats at
disconnect.

Mapping to this mod [mod]. Decision: **track only weapons usable in multiplayer** (single-player leftovers such as
Garand, BAR, FG42, Tesla, Speargun, Akimbo, Silencer, Snooperscope and the generic monster attacks are excluded).
Draft mapping from OSP's 19 slots, based on what `g_client.c` hands out at spawn and the means-of-death list in
`bg_public.h` (to be verified when implementing):

| OSP slot | Mod weapon(s) | Means of death used for kills/deaths |
|---|---|---|
| Knife | `WP_KNIFE` (also thrown knives, `g_throwableKnives`) | `MOD_KNIFE`, `MOD_KNIFE_STEALTH`, `MOD_KNIFE2` |
| Luger / Colt | `WP_LUGER` / `WP_COLT` | `MOD_LUGER` / `MOD_COLT` |
| MP40 / Thompson / Sten | `WP_MP40` / `WP_THOMPSON` / `WP_STEN` | `MOD_MP40` / `MOD_THOMPSON` / `MOD_STEN` |
| Sniper Rifle | `WP_MAUSER` and its scoped `WP_SNIPERRIFLE` (both given to the Soldier) | `MOD_MAUSER`, `MOD_SNIPERRIFLE` |
| CrewGun | mounted MG42 (an entity, not a `weapon_t`) | `MOD_MACHINEGUN` [verify] |
| Panzerfaust | `WP_PANZERFAUST` | `MOD_PANZERFAUST` / `MOD_ROCKET`+`_SPLASH` [verify] |
| Venom | `WP_VENOM` | `MOD_VENOM` |
| Flamethrower | `WP_FLAMETHROWER` | `MOD_FLAMETHROWER` |
| Grenade | `WP_GRENADE_LAUNCHER` (Axis) / `WP_GRENADE_PINEAPPLE` (Allies) | `MOD_GRENADE`, `MOD_GRENADE_SPLASH`, `MOD_GRENADE_LAUNCHER`, `MOD_GRENADE_PINEAPPLE` |
| Dynamite | `WP_DYNAMITE` | `MOD_DYNAMITE`, `MOD_DYNAMITE_SPLASH` |
| Airstrike | smoke-grenade canister (`WP_SMOKE_GRENADE`) | `MOD_AIRSTRIKE` |
| Artillery | binoculars (`WP_ARTY`, `WP_BINOCULARS`) | `MOD_ARTY` |
| Syringe | `WP_MEDIC_SYRINGE` | hits = revives; attempts need a new counter |
| Health Packs | `WP_MEDKIT` | hits = used, attempts = dropped |
| Ammo Packs | `WP_AMMO` | hits = used, attempts = dropped |

Not player weapon rows:
- **Mortar:** `WP_MORTAR` is only fired by map entities (e.g. the beach mortar; `fire_mortar` sets the owner to the
  map's shooter entity), so a mortar death has no player attacker. `MOD_MORTAR` / `MOD_MORTAR_SPLASH` are world deaths.
- Pliers have no stat.
- Other world or environmental deaths: `MOD_WATER`, `MOD_SLIME`, `MOD_LAVA`, `MOD_CRUSH`, `MOD_FALLING`,
  `MOD_TRIGGER_HURT`, `MOD_TELEFRAG`, `MOD_EXPLOSIVE`, `MOD_SUICIDE` (and the map mortar above).

**Decided: world deaths are a pseudo-weapon row.** A "World" row in the weapon table has deaths only (no hits,
attempts, kills or headshots). Map mortar, falling, water, lava, crush, `trigger_hurt`, telefrag and `\kill` land there.
Rules:
- Every death is counted in exactly one row (the weapon that did it, or World), so per-weapon deaths always sum to `Dth`.
- `Sui` is a separate tally of the self-inflicted subset (e.g. own grenade, `\kill`); it is not an extra row.
- World deaths count in `Dth`, so they lower `Eff = Kll / (Kll + Dth)`. OSP's exact handling is not in the docs seen.
- The stats record may keep the finer breakdown by means of death (mortar vs falling vs water) for later use; the
  printed table shows one World line.
The mod's own extras (goombas, poisons, `MOD_KICKED`, knife kills) stay as separate counters outside the OSP rows.
Instagib mode uses an unscoped Mauser and would map to the Sniper Rifle slot.

## id Software's original ET source (primary reference; supersedes the ET:Legacy notes below where they differ)
Repository: github.com/id-Software/Enemy-Territory (`src/game/g_match.c`, `src/cgame/cg_window.c`,
`src/cgame/cg_servercmds.c`, `src/cgame/cg_main.c`). Confirmed by reading it:
- **`RoundStats()` in our `g_main.c` is adapted from id's `G_printMatchInfo`**: the row and totals format strings are
  identical (`"%-10s %s%-15s^3%4d%4d%4d%3d%s%4d^3%4d^2%6d^1%6d^6%5d^3%7d"`, the `ref` variable, `aTeams[]`).
  id's header is `TEAM   Player          Kll Dth Sui TK Eff  GP    DG    DR   TD  Score` (column `GP`); ours says
  `GB`; OSP says `Gib`. **id's ET has no assists column**; assists exist only in ET:Legacy.
- `ws` / `wws` weapon table (`CG_parseWeaponStats_cmd`): title `^7Overall stats for: ^3<name> ^7(^2<N>^7 Round<s>)`
  (OSP's window said `Accuracy info for:`), short header `Weapon     Acrcy Hits/Atts Kll Dth HS`, dashed separator in the
  full form, `^3No weapon info available.`, rows `^3%-9s: ` + `^7%5.1f ^5%4d/%-4d ` + `^2` kills + `^1` deaths + `^3` HS,
  then `^3Damage Given: ^7..  ^3Team Damage: ^7..` and `^3Damage Recvd: ^7..`. Rank/XP/skills lines are ET-only.
- Best/worst shots: `^2BEST Match Accuracies:`, header `^3WP   Acrcy Hits/Atts Kll Dth`, per-weapon "astats" lists, and
  `^3No qualifying topshot info available.` built at runtime from `"No qualifying %sshot info available."`.
- Windows (`cg_window.c`): fill `{0.3, 0.45, 0.3, 0.5}`, border `{0.5, 0.35, 0.25, 0.5}`, flash fill `{0.305, 0.475, 0.305,
  0.48}`. **Stats window** x=4, y=-100 (intermission x=10, y=-20), font scale `cf_wstats * 0.2`, fade-in 110 ms.
  **Topshots window** is used in id's ET: x=-20, y=-60 (intermission x=-10, y=-20), flash + fade + scroll-up 190 ms,
  scale `cf_wtopshots * 0.2`. Negative x/y are measured from the right/bottom edge of the 640x480 space.
  Defaults in `cg_main.c`: `cf_wstats` 1.2, `cf_wtopshots` 1.0 (OSP's docs say 0.8 for topshots).
- Demo overlays: timescale `^3TimeScale: ^7%.1f` at (42, 400) in a dark-green box with a yellow border, shown while
  the scale was recently changed (`demo_drawTimeScale`); AVI status at (42, 425). Both match OSP's DemoControls doc
  (lower-left readout). The `Time Scale: ^3%.1fx` wording I noted earlier is ET:Legacy's, not id's.
- **Licence (needs a decision before copying any ET code; not legal advice):**
  - id's GitHub releases of **ET** and **RTCW-MP** (2010) are the same kind: GPL with id's "additional terms"
    (available from id on request), some excluded third-party code, and game data still under the original EULA.
  - But `mod/` in this repo carries the **2001 "RTCW Game Source License"** (`mod/RTCW Game Source License.rtf`), a
    different, older agreement: free, non-commercial, electronic-only distribution; mods only for use with the full
    retail RTCW; no selling, renting or pay-per-play. The engine under `iortcw/` is GPLv3 (`iortcw/COPYING.txt`).
  - Which one governs the mod code depends on which release it came from (2001 or 2010). Mixing GPL code into code that
    is under the 2001 terms is the problematic combination. Reading ET as a reference is fine either way.
  - Not selling it helps with the 2001 terms (non-commercial) but does not change the GPL's obligations when
    distributing, and it does not change copyright on assets such as fonts.
  - Also worth checking against the 2001 terms: the browser build plays with `demopak0.pk3` (the demo), while the
    licence says mods are for use with the full game.

### ET fonts as a possible backport (idea, not started)
- id's ET text uses `limboFont1` = **`ariblk` 27pt** (Arial Black, plus a 16pt variant) and `limboFont2` = **`courbd`
  30pt** (Courier Bold). The `+wstats` window is drawn with `limboFont2` through `CG_Text_Paint_Ext`, so the ET stats
  window is a monospaced bold TrueType look with proper alignment, unlike OSP's wide-spaced charset text.
- Our mod already has the matching text path: `CG_Text_Paint_Ext` and `fontInfo_t` in `cg_draw.c`, and
  `trap_R_RegisterFont`. Menus register fonts such as `fonts/impact.ttf` and `fonts/smallFont` (`hud.menu`, `main.menu`).
- The engine renderer (`iortcw/code/renderer/tr_font.c`) loads **pre-baked** `fonts/<name>_<size>.dat` + image files.
  Making new ones needs a build with `BUILD_FREETYPE` to render a TTF offline. No font files ship in `assets/`.
- **Licence is the real risk.** ET's `ariblk` and `courbd` are Arial Black and Courier New Bold (Microsoft fonts), and
  ET's pre-baked font files are game data under the ET EULA, so do not copy them. Free look-alikes (SIL OFL fonts such as
  Liberation Mono Bold for Courier New and Archivo Black for Arial Black; check the licence text when picking) can be
  baked with the same sizes and give the same look legally.
- Unverified: whether ET's pre-baked `.dat` layout matches our engine's loader, and how the result looks in our HUD.
  A quick test is baking one candidate font and rendering the +wstats table with it in the headless harness.

## Existing `RoundStats()` vs OSP, and ET:Legacy as a reference
`RoundStats()` in `mod/src/game/g_main.c` already prints the end-of-round team tables to every client.
Checked by reproducing its format strings and comparing with the real OSP dumps:
- **Rows and Totals rows match OSP byte for byte** (column widths, 15-char colour-stripped names, truncated Eff,
  score-sorted). The team label is `%-10s` of a colour-coded string, so the colour codes eat the padding and the
  visible result is `Axis   ` / `Allies ` exactly as in OSP. Tested on four rows plus a totals row.
- **Header:** the mod prints `GB`; OSP prints `Gib` (renamed from GS in 0.9). Same column width otherwise.
- Separator is 69 dashes in both. Header length is 69 in both.
- **Colours:** the mod uses yellow (`^3`) for Kll/Dth/Sui/TK/Gib/Score, green (`^2`) for DG, red (`^1`) for DR,
  magenta (`^6`) for TD and cyan (`^5`) for the Totals label and efficiency. ET:Legacy uses the same scheme in the same
  columns (DG `^2`, DR `^1`, TD `^6`, Score `^3`, Totals `^5`). The OSP dumps we have are plain text, so OSP's own
  colours are not directly confirmed, but ET descends from OSP and the match is strong evidence.
- Not printed by the mod: the per-player `Accuracy info for:` block and the `Stats recorded:` footer.

ET:Legacy (GPL) is a useful reference, since OSP's author carried much of this into Enemy Territory. Its code
(`src/game/g_match.c`, `src/cgame/cg_window.c`, `src/cgame/cg_servercmds.c`) shows:
- `G_printMatchInfo` is the same table, extended with GUID, TmX/TmL/TmP, **Ast (assists)**, TG, TDG/TDR and optional
  rating columns. It uses `SK` for self kills where OSP used `Sui`.
- Weapon stats go to clients as one compact `ws` command and are logged as `WeaponStats:`, the same line OSP
  documented; per weapon `"%d %d %d %d %d"` = hits, attempts, kills, deaths, headshots, followed by damage given/
  received, team damage given/received, gibs, assists, self kills, team kills, team gibs, play-time %.
- The `+wstats` window system uses **the same cvar names** (`cf_wstats`, `cf_wtopshots`). Font scale is the cvar
  times 0.2. Default fill colour `{0.3, 0.45, 0.3, 0.5}` (the green tint in the OSP screenshot) and border
  `{0.5, 0.35, 0.25, 0.5}`. Position is x=4, y=-100 normally, x=10, y=-20 at intermission (negative = from the
  bottom). Text appears with a short fade-in. ET marks its topshots window unused.
- Window text colours: title `^7Overall stats for: ^3<name> ^7(^2<N>^7 Round<s>)`, header white, `^3No weapon info
  available.`, and rows `^3` weapon name, `^7` accuracy, `^5` hits/atts, `^2` kills, `^1` deaths, `^3` headshots.
  OSP's screenshot has the same name-yellow / round-count-green title, so these are probably OSP's colours too.
- ET differs from OSP in wording: `Overall stats for` vs OSP's `Accuracy info for`, `Hits/Shts` vs `Hits/Atts`, and a
  dashed separator (OSP's window showed dots). Follow OSP's wording, ET's structure and colours.
- Demo timescale readout in ET:Legacy: `^7Time Scale: ^3%.1fx` on a `{0, 0, 0, 0.6}` background (id's original differs, see above).
- License: ET:Legacy is GPL. Use it as a reference for formats and colours and write our own code; check licence
  compatibility with this mod's source licence before copying any code.

## Current state of the mod [mod]
`clientPersistant_t` (`mod/src/game/g_local.h`) already has: kills, deaths, teamKills, suicides, gibs, teamGibs,
revives, medPacks, ammoPacks (counted when someone picks the pack up, `g_items.c`), acc_shots, acc_hits,
headshots, damage_given, damage_received, team_damage, goomba, poison, knifeKill, spree maxima.
`Cmd_Stats_f` (`g_cmds.c`) prints a simple self-only summary.
`g_lua.c` has a commented `TODO: sess.aWeaponStats`.

| Needed for OSP parity | Status |
|---|---|
| Kll/Dth/Sui/TK/Gib/DG/DR/TD/Score + Eff + totals row | done: `RoundStats()` matches OSP rows; header says `GB` (OSP: `Gib`) |
| Per-weapon: attempts, hits, kills, deaths, headshots | missing |
| Death attributed to the killer's weapon (victim side) | missing |
| Syringe attempts (hits = revives) | attempts missing |
| Packs thrown (second number of 30/42) | missing |
| Per-round slices and "(N Rounds)" | missing |
| Stats record at disconnect / map change | missing |
| `stats [id]`, `statsall`, `scores`, `statsdump` | missing |
| `+wstats` / `+wtopshots` + server replies | missing |
| Assists (not in OSP; ET:Legacy has `kill_assists`) | missing, optional |
| Kill matrix | missing, optional |

## Design
### One stats record, three consumers
Per match, per player (humans; bots flagged): totals, a list of weapon rows
`{weapon, atts, hits, kills, deaths, headshots, damage}`, pack/revive counters, objective events, per-round
slices, final score, rounds played. The same record feeds:
1. in-game `stats` / `statsall` / `scores` text in the OSP layout,
2. a stats file written at intermission (and on disconnect) next to the replay sidecar,
3. backend ingest (JSON). Key: replay base name + account ID, so ingest is idempotent.

### Hooks needed in qagame
- Fire path: count an attempt per weapon (not when gibbing bodies; projectiles count at fire time).
- Hit path: count a hit per weapon (projectiles at explosion damage; reuse `LogAccuracyHit`).
- Kill path: killer's kill and victim's death keyed by means-of-death -> weapon bucket
  (grenade, panzer, flamethrower, knife, artillery, mines, dynamite, ...).
- Syringe attempts; pack thrown counters.
- Per-victim, per-attacker damage table with timestamps if assists are wanted.
- Flush on `ClientDisconnect` and level change; reset correctly per round.

### Tests
Use the two real dumps as golden output for the printer (format, truncation, sorting, blank cells).
Strict invariants: weapon kills sum to kills; weapon deaths sum to deaths (when suicides are 0).
Do not assert cross-team DG/DR equality.

## Client overlays (`+wstats`, `+wtopshots`)
From a screenshot of OSP at 1280x1024 [observed]:
- `+wstats`: translucent green-tinted box at the bottom left, above the health bar. Title
  `Accuracy info for: <name> (N Rounds)` (name yellow, count green), header
  `Weapon  Acrcy  Hits/Atts  Kll  Dth  HS`, a **dotted** separator, weapon rows, or
  `No weapon info available.` in yellow.
- `+wtopshots`: similar box at the bottom right above the ammo count, smaller text,
  `No qualifying topshot info available.` when empty.
- Text is widely spaced (narrow glyphs on a wide fixed advance): that is the OSP look, not a bug.
- Approximate layout in 640x480 virtual units: left box x 25-375, right box x 385-615, near the bottom,
  line pitch about 12. Tune against screenshots from the headless harness.

Implementation sketch:
- Server: reply to a client request (sent on key-down, refreshed about once a second while held) with a compact
  per-weapon list for the player or the followed player. Topshots: best accuracy per weapon among players over a
  minimum number of shots (cvar; OSP's threshold unknown).
- Client: `+wstats`/`-wstats`, `+wtopshots`/`-wtopshots`, parse replies, draw a translucent rect + text scaled by
  `cf_wstats` (1.2) and `cf_wtopshots` (0.8).
- Server decides who may see whose stats when following.

## Scoreboard additions (OSP screenshot, 1280x1024)
The base layout already exists in `cg_scoreboard.c` (Goals panel, `Mission time:`, `Reinforce time:`,
`Axis [n] (n players)`, Class/Ping columns, `Soldr`). What the OSP screenshot adds on top [observed, history 0.3/0.9]:
- Local date and time at the top right of the Goals panel (`22:06:48 . 16 Oct 2026`) [history 0.3].
- `Avg Ping` over each team's ping column [history 0.9].
- A ready marker left of each name during warmup: red `?` = not ready, green `!` = ready [history 0.9].
- `Mission time:` shows `Warmup` in yellow during warmup.
- Spectator ping on the scoreboard [history 0.9] and window prompts at the bottom during intermission
  [history 0.3] are not visible in this shot.
- The `+wstats` / `+wtopshots` windows are drawn on top of the scoreboard and stay visible while it is up, with a
  translucent tint so the scoreboard shows through. They overlap the bottom of the left team panel.
Not yet checked: which of these our scoreboard already draws (`Avg` is not in `cg_scoreboard.c`); needs a capture of
our scoreboard at 1280x1024 for a side-by-side.

## Fonts
- A screenshot shows OSP's text looks the same as ours, so the OSP sheets themselves are fine.
- The real gap: OSP lets you switch fonts live (`ch_font 0|1|2`). Ours (`cg_ospFonts` 0|1, `cg_main.c`) is read only
  at cgame init. Fix: register both sheets at init and swap handles when the cvar changes; consider a
  `ch_font`-compatible cvar once the mapping of 1 and 2 to `hudchars_OSP1/2` is known.
- Open: with the cvar on, the mod uses OSP1 for the small charset and OSP2 for the menu charset at the same time.
  OSP probably used one font at a time. Unconfirmed.

## Weapon icons (open)
The user reports OSP's weapon icons look crisper than ours. Not investigated yet. Candidate causes to check:
icon texture resolution and the shader flags (`nopicmip`, `nomipmaps`), texture filtering, and how the icon quad
is scaled relative to real pixels. Needs a side-by-side capture at the same resolution.

## Backend direction (summary)
- Postgres: `accounts`, `games`, `game_players` (one row per account per game), per-weapon rows, highlights.
- The ingest worker reads the stats files; games list is paginated; summaries kept forever, full replays 24-48h.
- Website: "my games" (stats + replay while it exists), a stats and breakdowns page, topshots leaderboards across
  games, weekly summary emails (opt-in).
- Details in `PLAN_launcher_accounts.md` and the discussion that produced this file.

## Replay viewer: OSP demo features worth matching [doc]
Fixed key layout that cannot break playback; timescale reset/step with an on-screen readout; controls window
toggle; scoreboard on Tab; zoom when following; AVI/MP4 capture presets; multiview (several players at once,
grouped by team); demo browser with directory navigation. Seeking backward needs periodic world-state keyframes
(proposed replay format v9).

## Phases
1. Per-weapon stats structure, hooks, reset/flush rules, and the OSP-format `stats`/`statsall`/`scores` printer,
   tested against the golden dumps.
2. Stats record file at intermission and disconnect; `statsdump`.
3. `+wstats` / `+wtopshots` server replies and client overlays; live font switching.
4. Backend: Postgres, ingest, paginated games list, replay retention to 48h, clip trimming.
5. Accounts and login; browser auto-login; launcher login; "my games", stats pages, weekly summaries.
6. Replay viewer v9.

## Open questions
- `osp-wolf-Multiview.txt` and the server-side readme (StatsInfo has been received).
- What `ch_font` was set to in the OSP screenshot, and what values 1 and 2 map to.
- OSP's minimum-shots threshold for topshots.
- Whether assists and a kill matrix are wanted beyond OSP parity.
- Weapon icon crispness: cause unknown.

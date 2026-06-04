# Cleanup Plan

## Goal

Reduce the project to an intentional product tree that is easier to build, reason about, and extend.

## Current active build graph

These paths appear to be part of the current Docker-based workflow:

- `Dockerfile`
- `docker-compose.yml`
- `docker/`
- `iortcw/`
- `0.83/GameInterfaces/RTCW/src/`
- `0.83/GameInterfaces/RTCW/main/`
- `0.83/Installer/Files/rtcw/`
- `0.83/Omnibot/Common/`
- `0.83/Omnibot/RTCW/`
- selected vendored dependencies under `0.83/Omnibot/dependencies/`
- `web/`

Everything else should be treated as potentially legacy until proven necessary.

## Near-term cleanup ideas

### 1. Consolidate third-party code

- Create a single repo-owned location for vendored libraries, such as `third_party/`.
- Move active dependencies there one by one instead of relying on copies buried in unrelated projects.
- Start with `zlib`, since there are currently multiple copies in-tree.
- Add a small `README.md` per vendored library with:
  - upstream name and version
  - source URL or provenance
  - local patches, if any
  - which targets consume it

### 2. Document the real build graph

- Write a short build inventory describing which directories produce:
  - server runtime
  - client mod binaries
  - bot shared library
  - web/download artifacts
- Note which build system owns each output:
  - `make`
  - `bjam`
  - `cmake`
  - Docker stages

### 3. Separate active code from archive code

- Introduce clear top-level buckets such as:
  - `engine/`
  - `mod/`
  - `bot/`
  - `assets/`
  - `third_party/`
  - `legacy/`
- Do not move everything at once. Start by labeling what is active versus historical.

### 4. Inventory unused directories

- Audit directories that look tooling- or archive-related and verify whether they are used at all:
  - `0.83/Tools/`
  - `0.83/GameInterfaces/RTCW/Tools/`
  - `0.83/GameInterfaces/RTCW/src/downlib/`
  - `0.83/GameInterfaces/RTCW/src/extractfuncs/`
  - old Visual Studio project trees under `0.83/Omnibot/projects/`
- Tag them as:
  - active
  - legacy but needed for release
  - removable

### 5. Reduce duplicate build definitions

- The project currently mixes:
  - Docker orchestration
  - `make`
  - `bjam`
  - `cmake`
  - legacy IDE project files
- Decide which build descriptions are authoritative for ongoing development.
- Prefer keeping legacy IDE files only if someone actively uses them.

### 6. Untangle shipping content from source

- Identify what in `0.83/Installer/Files/rtcw/` is:
  - shipped runtime content
  - bot assets
  - generated packaging material
  - old installer baggage
- Consider moving runtime assets into a clearer content tree and generating packaged outputs from there.

### 7. Formalize module boundaries

- Define which code belongs to:
  - engine changes
  - qagame logic
  - cgame/ui client module changes
  - omnibot integration
- This is especially important for replay/highlight work so server-only features do not leak into the wrong layer.

## Replay-specific cleanup follow-ups

- Move replay code behind a dedicated module boundary in `qagame` rather than continuing to grow hooks directly in unrelated gameplay files.
- Define a small documented replay archive format:
  - file header
  - chunk layout
  - sample layout
  - event layout
  - versioning rules
- Add a quick archive inspection tool later so replay files can be analyzed without reading server code.

## Suggested phased approach

### Phase 1

- Consolidate `zlib`
- Document the active build graph
- Mark likely legacy directories

### Phase 2

- Create a cleaner source layout plan
- Move or alias active code into intentional roots
- Reduce duplicate build definitions where possible

### Phase 3

- Remove or archive dead code
- Add dependency provenance docs
- Add tooling for replay archive inspection and validation

## Rules to keep cleanup safe

- Do not delete anything solely because it looks old.
- First prove whether a path is in the live Docker build, runtime image, or packaging flow.
- Prefer small relocations with preserved behavior over sweeping repo surgery.
- Keep each cleanup change independently buildable in Docker.

# Legacy Tree

`legacy/` contains archived project material that is not part of the default Docker build/runtime path.

This first cleanup pass is conservative:

- files were moved here only when they were clearly outside the current top-level workflow, or when they were legacy support material such as tool/project trees
- compatibility symlinks were left behind where older paths may still be referenced indirectly
- nothing here should be deleted without verifying it is not needed for a later platform/tooling workflow

Current archived buckets include:

- `legacy/0.83/Tools`
- `legacy/0.83/Installer/Useful stuff`
- `legacy/0.83/GameInterfaces/RTCW/Tools`
- `legacy/0.83/GameInterfaces/RTCW/lua-scripts`
- `legacy/0.83/Omnibot/projects`
- `legacy/0.83/Omnibot/linux`

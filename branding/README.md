# S4NDMoD26 branding

The mark takes the structure of the Wolfenstein eagle (flat stepped wing bars with slanted tips, shoulder
blocks with a hooked beak facing the centre, a body of slabs with diagonal cut ends) and makes the body
the numerals "26".  Each numeral's top bar is the end of the lower wing bar, so the mark is one piece.

| File | Use |
|---|---|
| `s4nd-mark.svg` | wide mark (header lockups).  Copies: `launcher/ui/logo.svg`, `infra/web/nginx/html/img/s4nd-logo.svg` |
| `s4nd-icon.svg` | square tile (favicon, app icon).  Copy: `infra/web/nginx/html/img/s4nd-icon.svg` |
| `s4nd.ico` | multi-size icon (16-256) embedded in the Windows launcher via `launcher/rsrc_windows_amd64.syso` |

Brand red is `#cf1b1f` (`--red` in `s4nd.css` and the launcher UI; `--red-hi #e8393c` for hover).  The PNGs in
`infra/web/nginx/html/img/` (favicon-32, apple-touch-icon, icon-512) are renders of `s4nd-icon.svg`.

Regenerating the `.syso` after changing `s4nd.ico`:
`go install github.com/akavel/rsrc@latest && rsrc -ico branding/s4nd.ico -arch amd64 -o launcher/rsrc_windows_amd64.syso`

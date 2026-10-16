# S4NDMoD26 branding

The mark takes the structure of the Wolfenstein eagle: flat stepped wing bars with slanted tips, narrow shoulder
blocks with a hooked beak facing the centre, and the original's cut-back detail where the top feather bar's
inner end is cut on a diagonal and merges into the second bar (a thin slot between them closes near the
shoulder).  The body is the numerals "26" in stencil slabs, their bottoms sloping to a V like the tapering W.
The lower wing bar runs on into each numeral's top bar, so the mark is one connected piece, and a thin light
backing outlines it as on the key art.

| File | Use |
|---|---|
| `s4nd-mark.svg` | wide mark (header lockups).  Copies: `launcher/ui/logo.svg`, `infra/web/nginx/html/img/s4nd-logo.svg` |
| `s4nd-icon.svg` | square tile (favicon, app icon).  Copy: `infra/web/nginx/html/img/s4nd-icon.svg` |
| `s4nd.ico` | multi-size icon (16-256) embedded in the Windows launcher via `launcher/rsrc_windows_amd64.syso` |

Brand red is `#cf1b1f` (`--red` in `s4nd.css` and the launcher UI; `--red-hi #e8393c` for hover).  The PNGs in
`infra/web/nginx/html/img/` (favicon-32, apple-touch-icon, icon-512) are renders of `s4nd-icon.svg`.

Regenerating the `.syso` after changing `s4nd.ico`:
`go install github.com/akavel/rsrc@latest && rsrc -ico branding/s4nd.ico -arch amd64 -o launcher/rsrc_windows_amd64.syso`

## Grid

One stroke weight (15) for every bar and every numeral stroke.  `x=248` is the outer edge of the body, of the
shoulder block and the end of the lower bar (and `x=152` on the left); the block's bottom is the second bar's
bottom; the lower bar's top is the numerals' top; every bar tip is cut at 45 degrees; the top bar's cut-back and
the slot's closing end are parallel.

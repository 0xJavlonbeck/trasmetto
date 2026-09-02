# Trasmetto Architecture

Trasmetto is a single-binary Go upload/download server. The command package is intentionally small; application behavior lives under `internal/`, and the browser UI lives under root-level `static/`.

## Runtime Flow

1. `cmd/trasmetto/main.go` parses configuration and starts `net/http`.
2. `internal/config` validates flags, upload limits, and timeout values.
3. `internal/server` registers routes and serves browser requests.
4. The module-root `embed.go` embeds `static/` so compiled binaries do not depend on loose asset files.

## Static Layout

- `static/html` stores server-rendered templates.
- `static/css/foundation` stores fonts, design tokens, and base element rules.
- `static/css/layout` stores page structure and responsive rules.
- `static/css/components` stores isolated component styling.
- `static/js` stores browser-side behavior.
- `static/fonts` stores embedded font files.
- `static/img` stores embedded image assets, including the local page background.

## Upload Policy

Uploaded files do not overwrite existing files by default. Collisions are saved
as `name(1).ext`, `name(2).ext`, and so on. When `--allow-replace` is enabled,
uploads replace the existing destination file instead.

## Size Limits

Uploads are streamed directly to the destination directory instead of being
spooled into multipart temp files first. By default there is no upload size
limit. `-max-upload-size` limits the total file bytes accepted by one multipart
upload request; use `0` to disable the limit.

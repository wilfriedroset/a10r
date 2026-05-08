# Security audit — 2026-05

Adversarial review of the v0.1 codebase. Scope: a TUI binary talking
HTTP(S) to Alertmanager / Mimir; no server, no multi-user surface.
Findings ranked by what actually matters for a single-user TUI;
"overkill" supply-chain items intentionally omitted.

## Conclusion

Per-package code is tight. The risk concentrates in **`cmd/tui.go`'s
wiring layer** and **`internal/backend/transport`'s redirect handling**
— close those two seams and most high-severity surface disappears.

Three of the four highs (F2, F3, F4) are the same root cause:
`runTUI` calls `config.Load` directly and never calls
`config.Resolve`, never instantiates `internal/log`, and never
plumbs `flags.ReadOnly` into the action registry / pages. The
plumbing exists; it just isn't connected. F1 is independent and
lives in the auth `RoundTripper`s.

## Findings

| # | Sev | Where | One-liner |
|---|-----|-------|-----------|
| F1 | HIGH | `internal/backend/transport/transport.go` | `basicRT` / `addHeaderRT` / `headersRT` re-inject auth on every `RoundTrip`, undoing Go's cross-origin redirect strip. Credentials leak to redirect targets. |
| F2 | HIGH | `cmd/tui.go:runTUI` | `--read-only` (and `defaults.read_only`, `backend.read_only`, `A10R_READ_ONLY`) is bound but never applied — dangerous bindings stay live in TUI mode. |
| F3 | HIGH | `cmd/tui.go:runTUI` | `config.Resolve` is never called — `--theme`, `--poll-interval`, `--log`, `--log-format`, `--debug`, `--quiet`, `--tenant`, `A10R_LOG`, `A10R_LOG_FORMAT` silently ignored. |
| F4 | HIGH | `cmd/tui.go:runTUI` | TUI uses `slog.Default()` (stderr); `internal/log.New` is never called. No persisted audit trail for silence write ops; `--log` is dead. |
| F5 | MED | `internal/tui/wizard/wizard.go` | First-run wizard writes plaintext credentials into `a10r.yaml` instead of nudging toward `${VAR}` interpolation. |
| F6 | MED | `internal/backend/transport/transport.go:buildTLSConfig` | Inline `tls_config.ca` replaces system roots silently (empty `x509.NewCertPool` + AppendCertsFromPEM). |
| F7 | MED | `internal/config/types.go:validTLSVersions` | Schema still accepts `TLS10` / `TLS11`. |
| F8 | MED | `internal/tui/page/silences/handlers.go:handleEditorFinished` | Post-edit YAML's `id:` is taken verbatim; no equality check against `pendingEdit.id`. A typo redirects the update to a different silence. |
| F9 | MED | `internal/backend/transport/transport.go:buildProxyFunc` | `proxy_from_environment: true` honored without surfacing the resolved proxy — env-var injection MITMs every backend. |
| F10 | LOW | `internal/tui/edit/edit.go:Edit` | Tempfile path is deterministic (`edit-<sanitize(id)>.yaml`); no `O_EXCL`, no symlink check. Cache dir mode 0o755. |
| F11 | LOW | `internal/backend/vanilla/{read,write}.go` | Silence ID concatenated into URL path without `url.PathEscape`. |
| F12 | LOW | `internal/tui/page/tenantconfig/tenantconfig.go:redactBasic` | `BasicAuth.Username` is not redacted in the inspector. |
| F13 | LOW | `internal/tui/theme/loader.go:findSkin` | `theme.name` is `filepath.Join`'d into UserDir without validation; `..` segments escape. |
| F14 | LOW | `internal/backend/vanilla/client.go:exec` | `json.NewDecoder(resp.Body).Decode(...)` with no `io.LimitReader`; hostile backend can OOM. |
| F15 | LOW | `internal/tui/edit/edit.go:Edit` | Editor subprocess inherits the parent env — including `${MIMIR_TOKEN}` etc. used for YAML interpolation. |
| F16 | LOW | `internal/tui/edit/edit.go:Edit` | `exec.CommandContext(context.Background(), …)` not tied to the program's ctx. |
| F17 | LOW | `internal/tui/page/silences/bulk.go` | Bulk-expire fanout logs `err.Error()` (server-controlled body) verbatim. |
| F18 | INFO | wizard `headers:` auth | Same redirect-replay path as F1; the F1 fix must cover `headersRT`, not just the three named auth blocks. |

## Attack chains worth keeping in mind

- **Redirect → bearer theft → silence storm.** Hostile / hijacked
  backend returns `302 Location: https://attacker/`. F1 replays the
  token. Attacker silences real alerts; F4 means there is no local
  log to reconstruct from.
- **`--read-only` bypass.** Operator on-call invokes `--read-only` and
  trusts the lock. F2 + F3 mean a misclick or a bracketed paste of
  `:silences\nx\ny` still writes.
- **HTTPS_PROXY hijack.** Co-worker's `.envrc` (or compromised CI
  image) sets `HTTPS_PROXY=http://attacker`; F9 + F1 hand over every
  credential transparently.
- **Editor symlink swap.** Local co-tenant pre-creates a symlink at
  `~/.cache/a10r/edit-<sil-id>.yaml`; F10 + F8 lets them rewrite a
  *different* silence's body when the operator presses `Ctrl+E`.

## Recommendations

Group fixes by seam so a follow-up PR per group lands cleanly.

### 1. Wire the resolver and the logger (fixes F2, F3, F4)

In `cmd/tui.go:runTUI`, before any page is built:

1. Call `effective, err := config.Resolve(CLIFlags{...}, os.Getenv, *cfg)`.
2. Build the logger via `internal/log.New(log.Opts{
     Path: effective.Config.Log.Path,
     Format: log.Format(effective.Config.Defaults.LogFormat),
     Level: levelFor(effective.Debug, effective.Quiet),
   })`, defer `closer.Close()`, `slog.SetDefault(logger)`.
3. Pass `effective.Config.Defaults.ReadOnly` into every page's
   `Options` and the help overlay; gate Dangerous handlers in
   `silences` / `alerts` / `groups` on it (early-return + flash hint).
4. Filter the action registry once via `registry.Filter(readOnly)` so
   the help overlay drops Dangerous entries.

Add tests:
- `--read-only` + `n` keypress on silences page asserts no form push.
- `--log /tmp/a.log` writes the silence-create record there.
- `A10R_READ_ONLY=true` propagates the same way as `--read-only`.

### 2. Stop replaying auth on redirects (fixes F1, F18)

In each of the four RoundTrippers (`basicRT`, `addHeaderRT`,
`headersRT`; `userAgentRT` is fine — UA is not sensitive):

- Capture the **expected host** at construction time (the parsed
  `cfg.BaseURL` host).
- In `RoundTrip`, only inject when `req.URL.Host == expectedHost`
  (case-insensitive). On mismatch, return the request unmodified —
  let the redirect proceed with no credentials, or fail.
- Belt-and-braces: set `http.Client.CheckRedirect` to a function that
  refuses cross-origin redirects with a clear error.

Regression test: spin up a httptest server that responds 302 to a
second httptest server, assert no `Authorization` header arrives at
the second.

### 3. Tighten transport defaults (fixes F6, F7, F14)

- Drop `TLS10` and `TLS11` from `validTLSVersions` (config error on
  configure). Default min stays Go's TLS 1.2.
- Either start the CA pool from `x509.SystemCertPool()` and append,
  or rename the field in docs to make "ca replaces system roots"
  explicit. Pick one; current behaviour is the surprising one.
- Wrap `resp.Body` in `io.LimitReader(resp.Body, 64<<20)` before the
  JSON decode in `vanilla.Client.exec`.

### 4. Editor round-trip hygiene (fixes F8, F10, F15, F16)

- After `silenceFromYAML`, refuse the update when
  `pending.id != ""` and `pending.id != id`; flash an error.
- Tempfile via `os.CreateTemp(root, "edit-"+sanitize(id)+"-*."+ext)`;
  reject when `os.Lstat` reports a symlink.
- Cache dir created at `0o700`.
- Editor `cmd.Env` filtered to a small allow-list (`HOME`, `PATH`,
  `TERM`, `LANG`, `LC_*`, `EDITOR`, `XDG_*`); `${MIMIR_TOKEN}`-style
  secrets are intentionally dropped.
- Pass the program ctx into `exec.CommandContext` instead of
  `context.Background()`.

### 5. Small polish (F5, F9, F11, F12, F13, F17)

- Wizard renders `${A10R_BACKEND_<NAME>_PASSWORD}` placeholders by
  default; print a one-shot block telling the user how to export.
- Log the resolved proxy (per backend, first request) when
  `proxy_from_environment: true` so the operator can see the chain.
- `url.PathEscape` silence IDs in `vanilla.{Get,Update,Expire}Silence`.
- Redact `BasicAuth.Username` in the tenant-config inspector.
- Validate `theme.name` against `^[a-zA-Z0-9_.-]+$`.
- Strip control characters and cap length on backend error strings
  before logging (`vanilla.classifyStatus` reads response body
  verbatim — anything the AM puts in the body lands in stderr).

## Out of scope for v0.1

- Cosign / SLSA provenance on releases. Reasonable for a published
  pet project once it leaves v0.1; not a v0.1 blocker.
- Strict env allow-list for `HTTPS_PROXY` host. A startup INFO log
  line is enough — the user is the trust boundary here.
- Cross-process file locking on the log path. Multiple `a10r`
  instances writing the same lumberjack file is unusual and the
  observable failure is interleaved lines, not a security issue.

## Sequencing suggestion

1. Group 1 (wiring) — biggest blast radius, cheapest fix; lands
   `--read-only` and `--log` as advertised.
2. Group 2 (redirect cred leak) — independent, isolated change in
   `transport`, easy to add a regression test.
3. Group 3 (transport defaults) — small additive changes.
4. Group 4 (editor) — touches one file plus a handler.
5. Group 5 (polish) — bundle into one commit if reviewer agrees.

## Resolution (2026-05-08)

Decisions taken on each finding after review.

### Address per audit (no modification)

| # | Sev | Note |
|---|-----|------|
| F1 | HIGH | Host-pin RTs + `CheckRedirect` refuses cross-origin |
| F2 | HIGH | Wire `ReadOnly` into pages + action registry filter |
| F3 | HIGH | Call `config.Resolve` from `runTUI` |
| F4 | HIGH | Init `internal/log`, `slog.SetDefault(...)` |
| F8 | MED | Refuse silence update on id mismatch + flash + reopen editor (buffer preserved) |
| F9 | LOW | Log resolved proxy at first request when `proxy_from_environment: true` |
| F11 | LOW | `url.PathEscape` silence IDs |
| F13 | LOW | Validate `theme.name` against `^[a-zA-Z0-9_.-]+$` |
| F14 | LOW | `io.LimitReader(.., 64<<20)` before JSON decode |
| F16 | LOW | Pass program ctx into `exec.CommandContext` |
| F17 | LOW | Strip control chars + cap length on backend error logs |
| F18 | INFO | Folds into F1 (`headersRT` covered by host-pinning) |

### Address with modification

| # | Sev | Modification |
|---|-----|--------------|
| F5 | MED | Accept plaintext; print one-line `${VAR}` interpolation hint after wizard write |
| F6 | MED | Keep replace-semantics (Prometheus parity); add doc + startup INFO log per backend |
| F7 | MED | Keep `TLS10`/`TLS11` in schema; emit WARN at config load when selected |
| F10 | LOW | `os.CreateTemp` random suffix + `Lstat` symlink reject + cache dir `0o700` |
| F12 | LOW | Partial-redact username: first 2 chars when `len ≥ 4`, else `***` |

### Accept silently

| # | Sev | Reason |
|---|-----|--------|
| F15 | LOW | User is the trust boundary; editor choice is on operator |

### PR sequencing

1. **Group 1** — F2/F3/F4 wiring
2. **Group 2** — F1/F18 redirect cred leak
3. **Group 3** — F6/F7/F14 transport defaults
4. **Group 4** — F8/F10/F16 editor round-trip
5. **Group 5** — F5/F9/F11/F12/F13/F17 polish

## Re-audit prompt

Paste this once the fixes have landed to retrigger an adversarial
review. The prior baseline lives at `docs/design/security-audit.md`
(this file).

> Adversarial security audit of a10r, a Go TUI for Alertmanager /
> Mimir. Threat model: hostile / hijacked backend, local co-tenant,
> insider with `a10r.yaml` access. Read
> `docs/design/security-audit.md` first — that is the prior baseline.
>
> 1. Verify each of F1–F18: confirm the fix is present, regression
>    test exists where the recommendation called for one, and the
>    fix actually closes the attack chain (don't trust commit
>    messages — read the code). Flag any partial or sidesteppable
>    fix.
> 2. Look for fresh issues introduced by the fixes themselves
>    (new wiring, new redirect handling, new logger init paths,
>    new editor sandboxing).
> 3. Re-walk the four attack chains and any new chain the fixes
>    expose.
>
> Output: short status table (F1–F18: fixed / partial / regressed /
> not-applicable-anymore), then any new findings in the same
> severity/where/one-liner format used in this doc, then updated
> attack chains. Stay tight — this is a TUI, not a server; don't
> bring back the items in "Out of scope for v0.1".

# 0008 — HTTP debug redaction via stdlib slog ReplaceAttr

`--debug-http` logs request/response metadata (method, URL, status,
latency, headers — no bodies) through structured slog attrs, and
secret-key masking is applied centrally by
`slog.HandlerOptions.ReplaceAttr` matching a fixed lowercase set
(`authorization`, `cookie`, `set-cookie`, `proxy-authorization`,
`password`, `token`, `bearer`, `credentials`, `api-key`,
`x-api-key`); the stdlib hook is the smallest centralised redactor
we can ship — one function, every future log site that uses these
attr keys gets masking for free. `X-Scope-OrgID` is intentionally
**not** masked because it is a Mimir routing identifier, not a
credential, and multi-tenant debug logs become unreadable when
every tenant's request looks identical. Body dumps are deferred to
a follow-up opt-in flag — body content is open-ended (alert
annotations, silence comments) and regex scrubbing exceeds the
v0.0.1 debugging value.

# 0050 — Pages read the live configuration

A page holds the one `*session.Session` and asks it for `read_only`,
the guardrails, the bulk pool size and `tui.poll_delta` at the point
of use, so `:reload` reaches every page on the stack through
`Session.Apply` rather than a message per changed field. Label columns
are the one exception, because they key the sorter and the header
geometry: a payload-free `ConfigReloadedMsg` asks those pages to
re-derive them. A read on demand costs a dereference per frame and
buys the end of the stale copy.

# Backends and topology

a10r is an Alertmanager client. It drives the Prometheus
[Alertmanager v2 HTTP API](https://github.com/prometheus/alertmanager)
(`/api/v2/alerts`, `/api/v2/silences`, `/api/v2/status`, …) and
nothing else. A backend works if — and only if — it speaks that
API.

## What that includes

| Target | Works? | How to point a10r at it |
| --- | --- | --- |
| Prometheus Alertmanager | Yes | `url:` is the Alertmanager root. |
| Grafana Mimir (alertmanager component) | Yes | `url:` is the Mimir root, `prefix: /alertmanager`, optional `tenant_header: X-Scope-OrgID` + `tenant:`. |

Both expose the same v2 surface; Mimir just mounts it under a
prefix and gates it behind a tenant header. See
[configuration.md](configuration.md) for the backend schema.

## The rule-evaluator topology

A common confusion: several tools in the alerting stack evaluate
alerting rules but are **not** Alertmanagers. They *send* alerts
to an Alertmanager; they do not serve the Alertmanager API
themselves. Pointing a10r at one of them does not work — there is
no `/api/v2/*` surface to read, and no silences to manage.

```text
  ┌──────────────────┐   fires alerts    ┌──────────────┐   reads    ┌──────┐
  │ rule evaluator   │ ────────────────► │ Alertmanager │ ◄───────── │ a10r │
  │ (Prometheus,     │   (notifier)      │ (or Mimir)   │  v2 API    └──────┘
  │  Loki ruler,     │                   └──────────────┘
  │  vmalert, …)     │
  └──────────────────┘
```

Point a10r at the **Alertmanager the evaluator notifies**, not at
the evaluator.

| Tool | What it is | a10r target |
| --- | --- | --- |
| Prometheus (rules) | rule evaluator, `alerting:` → Alertmanager | the Alertmanager it notifies |
| Loki ruler | rule evaluator, `-ruler.alertmanager-url` | that Alertmanager |
| VictoriaMetrics vmalert | rule evaluator, `-notifier.url` | that Alertmanager |
| Grafana Mimir ruler | rule evaluator | Mimir's alertmanager component (`prefix: /alertmanager`) |

Loki and vmalert do expose their own read-only rule/alert
endpoints (`/prometheus/api/v1/alerts`, vmalert's `/api/v1/alerts`),
but those are a different API shape — not the Alertmanager v2
surface, and with no silences, status, or grouping. a10r does not
read them.

## API version

a10r is **v2-only**. The Alertmanager v2 API has been the default
for years, so any current Alertmanager works. A legacy deployment
that exposes only the removed v1 API is not supported.

## Partial implementations

A backend that claims Alertmanager compatibility but implements
only part of the v2 surface (say, `/api/v2/alerts` but not
`/api/v2/silences` or `/api/v2/status`) will load and show alerts,
while silence and status features fail against the missing
endpoints. `a10r validate` and the startup probes catch the
clearly-broken cases (wrong URL, missing mount); a partial server
that answers some paths and 404s others surfaces per-feature at
runtime.

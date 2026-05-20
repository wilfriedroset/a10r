# 0029 — TLS cert/key fields reserved for future mTLS

`config.TLSConfig` carries `cert` and `key` string fields that the
YAML loader accepts at parse time but the validator rejects at
startup with a precise error naming the unsupported fields. The slot
is present in the schema today even though no code path consumes
it, so the day mTLS lands the schema is unchanged — only the
validator gate flips and `transport.NewBase` gains the cert/key
loading branch. Users who paste a `remote_write` block that already
carries `cert:` / `key:` learn at startup that the feature is not
yet wired, rather than silently shipping requests without a client
certificate or hitting an opaque TLS handshake failure on the first
poll.

The same posture applies to two other deferred auth methods. **OAuth2**
has no schema slot today; the day it lands, it joins
`basic_auth:` / `authorization:` / `bearer_token:` as a fourth peer
under each backend. **SigV4** has no schema slot today; when it
arrives it composes through the existing `transport.RoundTripper`
seam (the AWS SDK's `aws/v4` signer plugs in as another
`http.RoundTripper` layer). Both are additions, not breaking changes,
because `internal/backend/transport` is structured as composable
auth/header/TLS/proxy layers rather than a monolithic transport
config. The TLS `*_file` and `*_ref` variants (loading PEMs from disk
or a key store rather than inline) are reserved on the same terms —
v0.1 supports the inline `ca:` field only and a future deepening adds
the file/ref variants without touching call sites.

Considered and rejected: (a) drop the cert/key fields from the
schema and add them when mTLS lands — would force a schema break
(and a config-file migration for anyone who pasted from a Prometheus
config that carries them today); the up-front parse + late reject
shape pays a tiny validator cost in exchange for a non-breaking
future addition; (b) silently ignore cert/key when set — would
look like the fields work, leaving an operator to debug why their
mTLS request never actually presents a client certificate; (c) accept
cert/key today and fail at first TLS handshake — moves the error
from startup to first poll, well after the operator has stopped
watching, and `internal/transport.NewBase` would either need a
half-implemented mTLS path or a runtime panic — both worse than the
validator rejection.

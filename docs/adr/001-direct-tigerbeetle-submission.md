# ADR 001: Submit directly through the shared TigerBeetle client

Status: accepted

## Context

TigerBeetle's client is thread-safe, permits one in-flight request per client session, and
automatically combines concurrent compatible calls while a request is in flight. The ledger is
strictly serializable, and its account invariants make the funds decision atomically.

An application-owned in-memory queue would acknowledge work before it is durable, create a second
source of ordering and backpressure, and delay the result needed to truthfully approve an
authorization.

## Decision

HTTP operations use the API `request_id` directly as `Transfer.id`, call one shared TigerBeetle
client through a thin SDK adapter, and wait for the dense per-transfer result. There is no
application posting queue, idempotency database, or recovery worker.

After a process restart or lost HTTP response, the caller repeats the same command. TigerBeetle
returns `exists` for a previously created identical transfer, an `exists_with_different_*` result
for conflicting reuse, or `id_already_failed` for an ID whose earlier attempt was rejected by a
state-dependent error.

## Consequences

- An approval corresponds to a committed pending hold.
- Top-up and withdrawal responses describe the actual posted result.
- Concurrent calls are batched by the TigerBeetle client without a 50 ms application delay.
- If explicit micro-batching is later proven necessary, it can be added behind the adapter with a
  measured latency budget.
- A TigerBeetle call has no normal network timeout. HTTP cancellation does not imply that the
  financial command was canceled; callers recover by retrying the immutable transfer with the
  same ID.

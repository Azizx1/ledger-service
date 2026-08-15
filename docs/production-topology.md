# Production TigerBeetle topology

The production design uses one TigerBeetle cluster formatted with six replicas from the start.
The current TigerBeetle version cannot change a cluster's replica count after formatting, so the
single-replica development data file is disposable and is never promoted into production.

## Fault domains

Use three low-latency sites with two replicas in each site:

| Site | Replicas |
|---|---|
| A | 0, 1 |
| B | 2, 3 |
| C | 4, 5 |

Every replica requires its own machine and local data disk. Sites should be within a few
milliseconds because a transaction is replicated across sites before commit. A production
cluster uses a random, non-zero 128-bit cluster ID.

## Application connectivity

Every ledger-service process owns exactly one long-lived, thread-safe TigerBeetle client. The
client is configured with all six replica addresses in replica-index order and shared by all HTTP
handlers in that process. There is no ordinary database proxy or load balancer between the client
and replicas.

For example:

```text
TB_CLUSTER_ID=<random 128-bit hexadecimal id>
TB_ADDRESSES=tb-0:3000,tb-1:3000,tb-2:3000,tb-3:3000,tb-4:3000,tb-5:3000
```

Multiple stateless API instances may run across sites. Each API process creates one client
session; it does not create a client per request, account, or goroutine.

References:

- https://docs.tigerbeetle.com/operating/cluster/
- https://docs.tigerbeetle.com/operating/deploying/
- https://docs.tigerbeetle.com/coding/requests/

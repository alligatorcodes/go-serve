# Clustering Guide

Clustering uses HashiCorp Raft for leader election and replicated client assignment state. Each node has:

- A stable `cluster.node_id`.
- A Raft bind/advertise address.
- A persistent `cluster.data_dir` containing the Raft log, stable store, and snapshots.
- A public URL used when another node must receive a forwarded request.

## Consensus model

Use an odd number of voting nodes, normally three or five. Raft requires a quorum for writes and leader election:

```text
3 nodes: quorum 2
5 nodes: quorum 3
```

Do not run a production cluster with one voting node if availability matters. A two-node cluster cannot tolerate one failure while retaining quorum.

Set `bootstrap = true` only for the initial cluster bootstrap. The initial node plus the configured peers form the first Raft configuration. Subsequent nodes should join through a controlled operator workflow using the cluster join API or an equivalent administrative tool; do not bootstrap a second independent cluster.

For local development, `make run-cluster` starts the three configs under `config/example-cluster-node-*.toml` as separate processes. Node 1 bootstraps on Raft port `7001`; nodes 2 and 3 use their configured `admin_url` peer to request membership and listen on Raft ports `7002` and `7003`. Public ports are `8081`-`8083`, and admin ports are `9911`-`9913`. Stop the target before deleting the generated `data/example-cluster-node-*` directories to reset the cluster identity.

Keep `data_dir` on durable storage and back it up according to the recovery policy. Never share one data directory between nodes.

## Virtual IP ownership

The Go process does not directly add or remove an IP address. Linux address ownership requires host networking privileges and is intentionally delegated to `on_leader_command` and `on_follower_command`. These commands receive the values as positional arguments:

```text
argument 1: go-serve-vip
argument 2: configured virtual_ip
argument 3: configured interface
```

Use a small, audited wrapper that invokes `ip address add/del`, keepalived, or another host-integrated mechanism. The wrapper must be idempotent, run with least privilege, and verify that the node is actually ready before advertising the VIP. The VIP should point clients at the elected leader; ordinary clients should not connect to individual node addresses.

The leader state is Raft-derived. A follower runs the follower hook after losing leadership, and a newly elected leader runs the leader hook. Network convergence is not instantaneous, so configure ARP/NDP and failover timing for the deployment environment.

## Client assignment and forwarding

The leader maps a stable client key to a node using deterministic hashing and commits the assignment through Raft. The mapping contains only:

- Client key
- Node ID
- Public peer URL
- Expiration time

When a request lands on a node that is not the assigned node, the node forwards it to the assigned public URL and adds `X-Go-Serve-Forwarded: 1` to prevent forwarding loops. Request bodies and credentials are not stored in Raft. Forwarding should run over private TLS-protected networking in production.

The current client key is the explicit `X-Go-Serve-Client-Key` header when present, otherwise the source IP. Place a trusted edge in front of the cluster if source IP comes from a proxy, and ensure that untrusted clients cannot choose arbitrary assignment keys.

Assignments expire and are recreated by the leader. If the assigned node fails, health checks and normal operator policy should remove or replace that assignment. A future enhancement should add node liveness to assignment selection and expose reassignment audit events.

## Security and operations

- Restrict the Raft port to cluster members only.
- Authenticate and encrypt Raft traffic at the network layer or with a hardened transport wrapper.
- Restrict cluster admin endpoints to the admin network.
- Use separate persistent volumes for each node.
- Monitor leader changes, quorum loss, assignment count, forwarding failures, and VIP hook failures.
- Test loss of the leader, loss of a follower, a partition, stale storage, and a delayed VIP update before production.
- Never force two nodes to bootstrap independently with the same logical cluster identity.

## Example topology

```text
                 Virtual IP
                     |
                 node-1 (leader)
                /               \
       Raft node-2           Raft node-3
             \                 /
              replicated assignment state
```

The VIP provides ingress failover. Raft provides a single authoritative leader and shared assignment state. HTTP forwarding provides request affinity after ingress reaches a non-assigned node.
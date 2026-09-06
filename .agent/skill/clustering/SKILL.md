# Clustering Skill

## Purpose
Maintain safe Raft-based clustering and virtual-IP integration.

## Rules
- Prefer an odd number of voting nodes, normally 3 or 5.
- A 3-node cluster needs 2 nodes for quorum; a 5-node cluster needs 3.
- Do not treat a 2-node cluster as fault tolerant.
- Keep `cluster.node_id` stable.
- Each node needs its own durable `cluster.data_dir`.
- Never share a Raft data directory.
- Bootstrap the logical cluster only once.
- Restrict Raft networking to cluster members.
- Protect Raft traffic at the transport/network layer.
- Keep cluster-admin endpoints on the admin network.

## Raft state
The FSM should contain only small deterministic assignment state such as:
- client key,
- node ID,
- public peer URL,
- expiration time.

Never store request bodies, credentials, access tokens, cookies, or other secrets in Raft.

## VIP hooks
- Use least privilege.
- Keep wrappers small and auditable.
- Make add/remove operations idempotent.
- Verify readiness before advertising the VIP.

## Testing
Cover snapshot/restore and leader/follower transitions without requiring a live multi-node deployment where possible.

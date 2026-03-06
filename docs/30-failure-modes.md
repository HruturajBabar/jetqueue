# Failure modes & how JetQueue behaves

## 1) API crashes after DB commit but before publish
Without outbox: job is "queued" in DB but never enters the queue.
With outbox: outbox row persists; on restart publisher drains it and publishes.

## 2) Publish succeeds but API crashes before DB commit
We avoid this by not publishing outside the transaction flow. Outbox ensures publish is driven from DB state.

## 3) Worker crashes mid-processing
JetStream at-least-once means message can redeliver if not acked.
Worker must be idempotent: inbox/processed table prevents double effects.

## 4) Duplicate deliveries
Expected in at-least-once.
Inbox table (job_id/message_id) ensures we only apply side effects once.

## 5) SQLite contention
SQLite is fine for local MVP. WAL improves read/write concurrency, but high write concurrency is still limited.
We keep writes small and simple and can upgrade to Postgres later.

## 6) NATS restarts
JetStream persists streams to disk; messages remain and can be replayed to consumers.
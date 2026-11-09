CREATE TABLE producers (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL UNIQUE,
 token_hash TEXT UNIQUE,
 version INTEGER NOT NULL DEFAULT 1,
 created_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE TABLE research_tasks (
 id TEXT PRIMARY KEY,
 producer_id TEXT NOT NULL REFERENCES producers(id),
 topics TEXT NOT NULL,
 languages TEXT NOT NULL,
 target_count INTEGER NOT NULL CHECK(target_count>0),
 rules_version TEXT NOT NULL,
 rules TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('active','target_reached','paused_service','waiting_for_signals','cancelled')),
 is_current INTEGER NOT NULL DEFAULT 1 CHECK(is_current IN (0,1)),
 version INTEGER NOT NULL DEFAULT 1,
 created_at INTEGER NOT NULL DEFAULT (unixepoch()),
 updated_at INTEGER NOT NULL DEFAULT (unixepoch()),
 UNIQUE(id,producer_id)
);
CREATE UNIQUE INDEX research_current_task ON research_tasks(producer_id) WHERE is_current=1;
CREATE TABLE research_batches (
 id INTEGER PRIMARY KEY,
 producer_id TEXT NOT NULL REFERENCES producers(id),
 batch_id TEXT NOT NULL,
 task_id TEXT NOT NULL,
 rules_version TEXT NOT NULL,
 run_id TEXT NOT NULL,
 payload BLOB NOT NULL,
 payload_hash TEXT NOT NULL,
 reception BLOB NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending_validation' CHECK(status IN ('pending_validation','validating','completed','paused_service')),
 created_at INTEGER NOT NULL DEFAULT (unixepoch()),
 UNIQUE(producer_id,batch_id),
 FOREIGN KEY(task_id,producer_id) REFERENCES research_tasks(id,producer_id)
);
CREATE INDEX research_pending ON research_batches(status,id);
CREATE TABLE research_candidates (
 batch_row INTEGER NOT NULL REFERENCES research_batches(id),
 external_id TEXT NOT NULL,
 title TEXT NOT NULL,
 ordinal INTEGER NOT NULL,
 PRIMARY KEY(batch_row,external_id)
);
CREATE TABLE research_rate_limits (
 producer_id TEXT PRIMARY KEY REFERENCES producers(id),
 attempts INTEGER NOT NULL,
 expires INTEGER NOT NULL
);
ALTER TABLE audit_events ADD COLUMN object_id TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_events ADD COLUMN details TEXT NOT NULL DEFAULT '';

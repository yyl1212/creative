ALTER TABLE needs ADD COLUMN title TEXT NOT NULL DEFAULT '';
ALTER TABLE needs ADD COLUMN task TEXT NOT NULL DEFAULT '';
ALTER TABLE needs ADD COLUMN task_key TEXT NOT NULL DEFAULT '';
ALTER TABLE needs ADD COLUMN task_fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE needs ADD COLUMN rules_version TEXT NOT NULL DEFAULT '';
ALTER TABLE needs ADD COLUMN verified_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE needs ADD COLUMN valid_until INTEGER NOT NULL DEFAULT 0;
ALTER TABLE needs ADD COLUMN decision TEXT NOT NULL DEFAULT '{}';
ALTER TABLE needs ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0;
CREATE INDEX needs_qualification ON needs(status,rules_version,valid_until);
CREATE INDEX needs_task_fingerprint ON needs(task_fingerprint);
ALTER TABLE research_batches ADD COLUMN version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE research_batches ADD COLUMN raw_purged INTEGER NOT NULL DEFAULT 0;
ALTER TABLE research_batches ADD COLUMN metadata_purged INTEGER NOT NULL DEFAULT 0;
ALTER TABLE research_batches ADD COLUMN pause_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE research_candidates ADD COLUMN result TEXT;
ALTER TABLE research_candidates ADD COLUMN need_id INTEGER REFERENCES needs(id);
CREATE TABLE need_aliases (
 producer_id TEXT NOT NULL REFERENCES producers(id),
 external_id TEXT NOT NULL,
 need_id INTEGER REFERENCES needs(id),
 latest_batch INTEGER NOT NULL REFERENCES research_batches(id),
 PRIMARY KEY(producer_id,external_id)
);
INSERT INTO need_aliases(producer_id,external_id,latest_batch)
 SELECT b.producer_id,c.external_id,max(b.id) FROM research_candidates c JOIN research_batches b ON b.id=c.batch_row GROUP BY b.producer_id,c.external_id;
CREATE TABLE need_sources (
 need_id INTEGER NOT NULL REFERENCES needs(id),
 evidence_id TEXT NOT NULL,
 fingerprint TEXT NOT NULL,
 snapshot TEXT NOT NULL,
 raw BLOB,
 fetched_at INTEGER NOT NULL,
 PRIMARY KEY(need_id,evidence_id)
);
CREATE INDEX need_evidence_fingerprint ON need_sources(fingerprint);
CREATE TABLE research_worker (
 id INTEGER PRIMARY KEY CHECK(id=1), owner TEXT NOT NULL DEFAULT '',
 fence INTEGER NOT NULL DEFAULT 0, expires_at INTEGER NOT NULL DEFAULT 0
);
INSERT INTO research_worker(id) VALUES (1);
CREATE TABLE usage_records (
 id INTEGER PRIMARY KEY,
 origin TEXT NOT NULL CHECK(origin IN ('website','external')),
 producer_id TEXT REFERENCES producers(id),
 usage_id TEXT,
 provider TEXT NOT NULL,
 model TEXT,
 response_id TEXT,
 input_tokens INTEGER,
 output_tokens INTEGER,
 reported_cost TEXT,
 currency TEXT,
 billing_reference TEXT,
 cost_status TEXT NOT NULL CHECK(cost_status IN ('unknown','reported','conflict','verified')),
 created_at INTEGER NOT NULL DEFAULT (unixepoch()),
 UNIQUE(producer_id,usage_id)
);
CREATE INDEX usage_billing ON usage_records(provider,billing_reference);

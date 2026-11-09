CREATE TABLE development_records (
 need_id INTEGER PRIMARY KEY REFERENCES needs(id),
 version INTEGER NOT NULL DEFAULT 1,
 state TEXT NOT NULL CHECK(state IN ('approved','design_ready','developing','review_ready','launched','failed','rolled_back','unverified')),
 need_version INTEGER NOT NULL,
 verification_id INTEGER NOT NULL,
 approval TEXT NOT NULL,
 design TEXT NOT NULL DEFAULT '{}',
 result TEXT NOT NULL DEFAULT '{}',
 approved_by TEXT NOT NULL,
 updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE TABLE release_records (
 id INTEGER PRIMARY KEY,
 need_id INTEGER NOT NULL REFERENCES needs(id),
 record_version INTEGER NOT NULL,
 commit_sha TEXT NOT NULL,
 slug TEXT NOT NULL,
 origin TEXT NOT NULL,
 manifest_hash TEXT NOT NULL,
 manifest TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('verified','rolled_back','unverified','failed')),
 verified_at INTEGER NOT NULL,
 error TEXT NOT NULL DEFAULT '',
 UNIQUE(need_id,manifest_hash)
);
CREATE TABLE app_settings (
 name TEXT PRIMARY KEY,
 value TEXT NOT NULL,
 version INTEGER NOT NULL DEFAULT 1,
 updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);
ALTER TABLE usage_records ADD COLUMN version INTEGER NOT NULL DEFAULT 1;
CREATE TRIGGER usage_version AFTER UPDATE ON usage_records
WHEN NEW.version = OLD.version
BEGIN
    UPDATE usage_records SET version = version + 1 WHERE id = NEW.id;
END;
CREATE TABLE usage_reconciliations (
 usage_record INTEGER PRIMARY KEY REFERENCES usage_records(id),
 provider TEXT NOT NULL,
 amount TEXT NOT NULL,
 currency TEXT NOT NULL,
 billing_item TEXT NOT NULL,
 note TEXT NOT NULL,
 reviewed_by TEXT NOT NULL,
 reviewed_at INTEGER NOT NULL DEFAULT (unixepoch()),
 UNIQUE(provider,billing_item)
);

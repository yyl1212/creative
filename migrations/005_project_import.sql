CREATE TABLE project_imports (
 batch_row INTEGER PRIMARY KEY REFERENCES research_batches(id),
 need_id INTEGER NOT NULL REFERENCES needs(id),
 verification_id INTEGER NOT NULL
);
CREATE INDEX project_import_need ON project_imports(need_id,verification_id);

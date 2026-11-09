package research

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yyl1212/creative/internal/auth"
	"github.com/yyl1212/creative/internal/jsonutil"
	"github.com/yyl1212/creative/internal/source"
	"github.com/yyl1212/creative/internal/usage"
	"github.com/yyl1212/creative/schemas"
)

type AgentReview struct {
	Provider      string    `json:"provider"`
	Model         string    `json:"model"`
	ReviewedAt    time.Time `json:"reviewed_at"`
	Reasoning     string    `json:"reasoning"`
	Deduplication string    `json:"deduplication"`
}
type projectReview struct {
	AgentReview
	Verdict         string `json:"verdict"`
	CatalogRevision string `json:"catalog_revision"`
	TaskKey         string `json:"task_key"`
	Evidence        []struct {
		ID      string `json:"evidence_id"`
		Valid   bool   `json:"valid"`
		Payment bool   `json:"payment"`
		Priced  bool   `json:"priced"`
		Reason  string `json:"reason"`
	} `json:"evidence"`
	Approval Approval `json:"approval"`
	Replaces *struct {
		NeedID  int `json:"need_id"`
		Version int `json:"version"`
	} `json:"replaces"`
}
type CatalogEntry struct {
	ID      int    `json:"id"`
	Version int    `json:"version"`
	Title   string `json:"title"`
	Task    string `json:"task"`
	TaskKey string `json:"task_key"`
	Status  string `json:"status"`
}
type Catalog struct {
	Revision string         `json:"revision"`
	Needs    []CatalogEntry `json:"needs"`
}

func projectTask(t *Task) bool {
	var r struct {
		Mode string `json:"decision_mode"`
	}
	return json.Unmarshal(t.Rules, &r) == nil && r.Mode == "agent_project"
}
func projectCatalog(tx *sql.Tx) (Catalog, error) {
	c := Catalog{Needs: []CatalogEntry{}}
	rows, e := tx.Query("SELECT id,version,title,task,task_key,status FROM needs ORDER BY id")
	if e != nil {
		return c, e
	}
	defer rows.Close()
	size := 0
	for rows.Next() {
		var n CatalogEntry
		if e = rows.Scan(&n.ID, &n.Version, &n.Title, &n.Task, &n.TaskKey, &n.Status); e != nil {
			return c, e
		}
		size += len(jsonText(n))
		if size > 96<<10 {
			return c, fail(409, "catalog_requires_review", "/catalog", "目录超出完整比对范围，请先安排分批复核，不能截断后判断无重复")
		}
		c.Needs = append(c.Needs, n)
	}
	c.Revision = fingerprint(jsonText(c.Needs))
	return c, rows.Err()
}
func (s *Service) ProjectCatalog(p auth.Producer) (Catalog, error) {
	tx, e := s.db.Begin()
	if e != nil {
		return Catalog{}, e
	}
	defer tx.Rollback()
	if e = verifyProducer(tx, p); e != nil {
		return Catalog{}, e
	}
	c, e := projectCatalog(tx)
	if e != nil {
		return c, e
	}
	return c, tx.Commit()
}

func (s *Service) parseProject(raw []byte) (batch, projectReview, error) {
	var b batch
	var p projectReview
	if len(raw) > 1<<20 {
		return b, p, fail(413, "body_too_large", "/", "请求不能超过 1 MiB")
	}
	if jsonutil.Check(raw) != nil {
		return b, p, fail(400, "invalid_json", "/", "JSON 无效或存在重复字段")
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	for _, name := range []string{"demand-batch.v1.schema.json", "project-package.v1.schema.json"} {
		data, e := schemas.Files.ReadFile(name)
		if e != nil {
			return b, p, e
		}
		doc, e := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if e != nil {
			return b, p, e
		}
		if e = compiler.AddResource(name, doc); e != nil {
			return b, p, e
		}
	}
	schema, e := compiler.Compile("project-package.v1.schema.json")
	if e != nil {
		return b, p, e
	}
	doc, e := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if e != nil {
		return b, p, e
	}
	if e = schema.Validate(doc); e != nil {
		return b, p, fail(422, "invalid_project_schema", "/", "立项包缺少字段或类型、格式无效")
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(raw, &fields); e != nil {
		return b, p, e
	}
	d := json.NewDecoder(bytes.NewReader(fields["project"]))
	d.DisallowUnknownFields()
	if e = d.Decode(&p); e != nil {
		return b, p, fail(422, "invalid_project_schema", "/project", "立项材料字段无效")
	}
	delete(fields, "project")
	fields["schema_version"] = json.RawMessage(`"creative.demand-batch.v1"`)
	base, e := json.Marshal(fields)
	if e != nil {
		return b, p, e
	}
	b, e = s.parse(base)
	if e != nil {
		return b, p, e
	}
	if secretPattern.MatchString(jsonText(doc)) {
		return b, p, fail(422, "secret_in_package", "/", "立项包不能包含凭据")
	}
	if e = validApproval(p.Approval); e != nil {
		return b, p, e
	}
	if p.Approval.VerificationID != 0 || b.RunReport.State != "submitted" {
		return b, p, lifecycleInput("审查时间须在最近 30 天内；核验 ID 由网站生成；立项包状态必须为 submitted")
	}
	return b, p, nil
}

// 模型已经作出业务判断；这里仅验证引用关系并把原样提交的结论映射到存储结构。
func projectDecision(c candidate, p projectReview, now time.Time) (Decision, []BoundSource, error) {
	d := Decision{Outcome: "qualified", Reasons: []string{}, TaskKey: normalized(p.TaskKey), TaskFingerprint: taskFingerprint(c), ValidUntil: p.ReviewedAt.AddDate(0, 0, 30), Agent: &p.AgentReview}
	if len(p.Evidence) != len(c.Evidence) {
		return d, nil, lifecycleInput("逐条结论与来源数量必须一致")
	}
	reviewed := map[string]bool{}
	for _, id := range p.Approval.ReviewedEvidence {
		if reviewed[id] {
			return d, nil, lifecycleInput("复核来源 ID 重复")
		}
		reviewed[id] = true
	}
	seen := map[string]bool{}
	var sources []BoundSource
	for _, ev := range c.Evidence {
		var ed EvidenceDecision
		found := false
		for _, v := range p.Evidence {
			if v.ID == ev.ID {
				if found || seen[v.ID] {
					return d, nil, lifecycleInput("证据结论 ID 重复")
				}
				found = true
				ed = EvidenceDecision{ID: v.ID, Valid: v.Valid, Payment: v.Payment, Priced: v.Priced, Reason: v.Reason}
			}
		}
		if !found || ed.Payment && !ed.Valid || ed.Priced && !ed.Payment || ed.Valid != reviewed[ev.ID] {
			return d, nil, lifecycleInput("证据结论、付费标记或复核关联不一致")
		}
		seen[ev.ID] = true
		delete(reviewed, ev.ID)
		observed, e := time.Parse(time.RFC3339, ev.ObservedAt)
		if e != nil || observed.After(now) || observed.After(p.ReviewedAt) {
			return d, nil, lifecycleInput("来源观察时间无效")
		}
		var published time.Time
		if ev.PublishedAt != nil {
			published, e = time.Parse(time.RFC3339, *ev.PublishedAt)
			if e != nil || published.After(observed) {
				return d, nil, lifecycleInput("原文时间无效")
			}
		}
		author := ""
		if ev.AuthorRef != nil {
			author = *ev.AuthorRef
		}
		if ed.Valid && (strings.TrimSpace(author) == "" || published.IsZero()) {
			return d, nil, lifecycleInput("有效来源必须有作者和明确发布时间")
		}
		if ed.Payment {
			expiry := published.AddDate(1, 0, 0)
			if !expiry.After(now) {
				return d, nil, lifecycleInput("付费来源已过期")
			}
			if expiry.Before(d.ValidUntil) {
				d.ValidUntil = expiry
			}
		}
		u, _ := url.Parse(ev.URL)
		site, e := source.SiteKey(u.Hostname())
		if e != nil {
			return d, nil, lifecycleInput("来源站点无效")
		}
		ed.AuthorKey = normalized(site + ":" + author)
		ed.Site = site
		ed.Fingerprint = fingerprint(ev.URL + "\n" + source.Normalize(ev.Quote))
		d.Evidence = append(d.Evidence, ed)
		sources = append(sources, BoundSource{ID: ev.ID, Document: source.Document{URL: ev.URL, Site: site, Hash: fingerprint(ev.Quote), FetchedAt: observed}, Block: source.Block{Text: ev.Quote, Author: author, AuthorRef: author, PublishedAt: published}})
	}
	if len(reviewed) != 0 {
		return d, nil, lifecycleInput("复核来源不存在")
	}
	return d, sources, nil
}

func (s *Service) ImportProject(p auth.Producer, raw []byte) ([]byte, error) {
	b, review, e := s.parseProject(raw)
	if e != nil {
		return nil, e
	}
	c := b.Candidates[0]
	now := time.Now()
	tx, e := s.db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = verifyProducer(tx, p); e != nil {
		return nil, e
	}
	// 先获得写锁，再读取目录版本，保证并发导入不能使用同一过期目录通过。
	if _, e = tx.Exec("UPDATE producers SET version=version WHERE id=?", p.ID); e != nil {
		return nil, e
	}
	digest := fingerprint(string(raw))
	var previous string
	var receipt []byte
	e = tx.QueryRow("SELECT payload_hash,reception FROM research_batches WHERE producer_id=? AND batch_id=?", p.ID, b.BatchID).Scan(&previous, &receipt)
	if e == nil {
		if previous != digest {
			return nil, fail(409, "batch_conflict", "/batch_id", "相同批次 ID 的字节不同")
		}
		return receipt, nil
	}
	if e != sql.ErrNoRows {
		return nil, e
	}
	if review.ReviewedAt.After(now) || review.ReviewedAt.Before(now.AddDate(0, 0, -30)) {
		return nil, lifecycleInput("审查时间须在最近 30 天内")
	}
	decision, sources, e := projectDecision(c, review, now)
	if e != nil {
		return nil, e
	}
	task, e := scanTask(tx.QueryRow("SELECT "+taskColumns+" FROM research_tasks WHERE id=? AND producer_id=?", b.TaskID, p.ID))
	if e == sql.ErrNoRows {
		return nil, fail(404, "task_not_found", "/task_id", "任务不存在")
	}
	if e != nil {
		return nil, e
	}
	if e = recomputeTasks(tx, now); e != nil {
		return nil, e
	}
	if e = hydrateTask(tx, task, now); e != nil {
		return nil, e
	}
	if !projectTask(task) || !task.Current || task.State != "active" || task.RemainingGap == 0 {
		return nil, fail(409, "project_task_inactive", "/task_id", "仅活动的新立项任务可导入")
	}
	if task.RulesVersion != b.RulesVersion {
		return nil, fail(409, "rules_changed", "/rules_version", "规则已变化")
	}
	catalog, e := projectCatalog(tx)
	if e != nil {
		return nil, e
	}
	if catalog.Revision != review.CatalogRevision {
		return nil, fail(409, "catalog_changed", "/project/catalog_revision", "目录已变化，请重新比较后生成新包")
	}
	needID, version := 0, 1
	if review.Replaces != nil {
		needID = review.Replaces.NeedID
		var owner bool
		var state, status string
		var oldVersion int
		// 重复候选也会产生 alias；只有最初的创建/导入记录能证明归属。
		e = tx.QueryRow(`SELECT n.version,n.status,coalesce(d.state,''),EXISTS(
 SELECT 1 FROM research_batches b WHERE b.producer_id=? AND b.id=coalesce(
  (SELECT i.batch_row FROM project_imports i WHERE i.need_id=n.id ORDER BY i.batch_row LIMIT 1),
  (SELECT v.batch_row FROM need_verifications v WHERE v.need_id=n.id ORDER BY v.id LIMIT 1)))
 FROM needs n LEFT JOIN development_records d ON d.need_id=n.id WHERE n.id=?`, p.ID, needID).Scan(&oldVersion, &status, &state, &owner)
		if e != nil && e != sql.ErrNoRows {
			return nil, e
		}
		if e == sql.ErrNoRows || !owner || oldVersion != review.Replaces.Version || status == "completed" || (state != "" && state != "approved" && state != "design_ready" && state != "unverified" && state != "failed") {
			return nil, fail(409, "project_version_conflict", "/project/replaces", "只能复核自有的尚未开工、失效或失败项目，且版本必须一致")
		}
		version = oldVersion + 1
	}
	var duplicate int
	e = tx.QueryRow("SELECT id FROM needs WHERE id!=? AND (task_key=? OR task_fingerprint=?) LIMIT 1", needID, decision.TaskKey, decision.TaskFingerprint).Scan(&duplicate)
	if e == nil {
		return nil, fail(409, "project_duplicate", "/project/task_key", "核心任务标识已存在，请整合原需求")
	}
	if e != sql.ErrNoRows {
		return nil, e
	}
	var alias *int
	e = tx.QueryRow("SELECT need_id FROM need_aliases WHERE producer_id=? AND external_id=?", p.ID, c.ExternalID).Scan(&alias)
	if e != nil && e != sql.ErrNoRows {
		return nil, e
	}
	if e == nil && (review.Replaces == nil || alias == nil || *alias != needID) {
		return nil, fail(409, "project_alias_conflict", "/candidates/0/external_id", "该外部 ID 已存在，请明确复核原需求")
	}
	if needID == 0 {
		r, err := tx.Exec("INSERT INTO needs(status,title,task,task_key,task_fingerprint,rules_version,verified_at,valid_until,decision,updated_at) VALUES ('qualified',?,?,?,?,?,?,?,?,?)", c.Title, c.Job, decision.TaskKey, decision.TaskFingerprint, rulesVersion, now.Unix(), decision.ValidUntil.Unix(), jsonText(decision), now.Unix())
		if err != nil {
			return nil, err
		}
		id, err := r.LastInsertId()
		if err != nil {
			return nil, err
		}
		needID = int(id)
	} else {
		if _, e = tx.Exec("UPDATE needs SET status='qualified',title=?,task=?,task_key=?,task_fingerprint=?,rules_version=?,verified_at=?,valid_until=?,decision=?,updated_at=?,version=? WHERE id=?", c.Title, c.Job, decision.TaskKey, decision.TaskFingerprint, rulesVersion, now.Unix(), decision.ValidUntil.Unix(), jsonText(decision), now.Unix(), version, needID); e != nil {
			return nil, e
		}
	}
	receipt = []byte(jsonText(map[string]any{"batch_id": b.BatchID, "status": "completed", "need_id": needID, "development_state": "approved", "receipt_url": "/api/research/v1/batches/" + b.BatchID}))
	r, e := tx.Exec("INSERT INTO research_batches(producer_id,batch_id,task_id,rules_version,run_id,payload,payload_hash,reception,status) VALUES (?,?,?,?,?,?,?,?,'completed')", p.ID, b.BatchID, b.TaskID, b.RulesVersion, b.RunID, raw, digest, receipt)
	if e != nil {
		return nil, e
	}
	batchID, e := r.LastInsertId()
	if e != nil {
		return nil, e
	}
	r, e = tx.Exec("INSERT INTO need_verifications(need_id,batch_row,external_id,decision,verified_at) VALUES (?,?,?,?,?)", needID, batchID, c.ExternalID, jsonText(decision), now.Unix())
	if e != nil {
		return nil, e
	}
	verification, e := r.LastInsertId()
	if e != nil {
		return nil, e
	}
	if _, e = tx.Exec("UPDATE needs SET verification_id=? WHERE id=?", verification, needID); e != nil {
		return nil, e
	}
	for i, v := range sources {
		if _, e = tx.Exec("INSERT INTO need_sources(need_id,verification_id,evidence_id,fingerprint,snapshot,fetched_at) VALUES (?,?,?,?,?,?)", needID, verification, v.ID, decision.Evidence[i].Fingerprint, jsonText(v), now.Unix()); e != nil {
			return nil, e
		}
	}
	result := Result{ExternalID: c.ExternalID, NeedID: &needID, Outcome: &decision.Outcome, ValidationState: "completed", ReasonCodes: []string{}, Issues: []string{}, Suggestions: []string{}}
	if _, e = tx.Exec("INSERT INTO research_candidates(batch_row,external_id,title,ordinal,result,need_id) VALUES (?,?,?,0,?,?)", batchID, c.ExternalID, c.Title, jsonText(result), needID); e != nil {
		return nil, e
	}
	if _, e = tx.Exec("INSERT INTO need_aliases(producer_id,external_id,need_id,latest_batch) VALUES (?,?,?,?) ON CONFLICT(producer_id,external_id) DO UPDATE SET need_id=excluded.need_id,latest_batch=excluded.latest_batch", p.ID, c.ExternalID, needID, batchID); e != nil {
		return nil, e
	}
	if _, e = tx.Exec("INSERT INTO project_imports(batch_row,need_id,verification_id) VALUES (?,?,?)", batchID, needID, verification); e != nil {
		return nil, e
	}
	review.Approval.VerificationID = int(verification)
	actor := "agent:" + p.ID + ":" + review.Provider + ":" + review.Model
	if _, e = tx.Exec("INSERT INTO development_records(need_id,state,need_version,verification_id,approval,approved_by) VALUES (?,'approved',?,?,?,?) ON CONFLICT(need_id) DO UPDATE SET state='approved',need_version=excluded.need_version,verification_id=excluded.verification_id,approval=excluded.approval,approved_by=excluded.approved_by,design='{}',result='{}',version=development_records.version+1,updated_at=unixepoch()", needID, version, verification, jsonText(review.Approval), actor); e != nil {
		return nil, e
	}
	if e = usage.RecordExternal(tx, p.ID, now.Unix(), b.RunReport.Usage); e != nil {
		return nil, e
	}
	if e = lifecycleAudit(tx, "project_imported", needID, version, actor, "research_api"); e != nil {
		return nil, e
	}
	if e = recomputeTasks(tx, now); e != nil {
		return nil, e
	}
	return receipt, tx.Commit()
}

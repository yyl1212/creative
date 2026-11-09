package research

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yyl1212/creative/internal/auth"
	"github.com/yyl1212/creative/internal/jsonutil"
	"github.com/yyl1212/creative/internal/usage"
	"github.com/yyl1212/creative/schemas"
)

type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Field + " " + e.Message }
func fail(status int, code, field, message string) *Error {
	return &Error{status, code, field, message}
}

type Service struct {
	db     *sql.DB
	schema *jsonschema.Schema
}

func New(db *sql.DB) (*Service, error) {
	b, err := schemas.Files.ReadFile("demand-batch.v1.schema.json")
	if err != nil {
		return nil, err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err = compiler.AddResource("demand-batch.v1.schema.json", doc); err != nil {
		return nil, err
	}
	schema, err := compiler.Compile("demand-batch.v1.schema.json")
	if err != nil {
		return nil, err
	}
	return &Service{db: db, schema: schema}, nil
}

func (s *Service) parse(raw []byte) (batch, error) {
	var b batch
	if len(raw) > 1<<20 {
		return b, fail(413, "body_too_large", "/", "请求不能超过 1 MiB")
	}
	if err := jsonutil.Check(raw); err != nil {
		return b, fail(400, "invalid_json", "/", "JSON 无效、重复字段、非 UTF-8 或嵌套过深")
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return b, fail(400, "invalid_json", "/", "JSON 无效")
	}
	if err = s.schema.Validate(doc); err != nil {
		path := "/"
		var ve *jsonschema.ValidationError
		if errors.As(err, &ve) {
			for len(ve.Causes) > 0 {
				ve = ve.Causes[0]
			}
			path = "/" + strings.Join(ve.InstanceLocation, "/")
		}
		return b, fail(422, "invalid_schema", path, "字段缺失、类型、长度、枚举或格式不符合 v1 Schema")
	}
	if err = json.Unmarshal(raw, &b); err != nil {
		return b, err
	}
	seen := map[string]bool{}
	for i, c := range b.Candidates {
		path := fmt.Sprintf("/candidates/%d", i)
		if seen[c.ExternalID] {
			return b, fail(422, "duplicate_id", path+"/external_id", "批内候选 ID 不能重复")
		}
		seen[c.ExternalID] = true
		evidenceIDs := map[string]bool{}
		for j, e := range c.Evidence {
			field := fmt.Sprintf("%s/evidence/%d", path, j)
			if evidenceIDs[e.ID] {
				return b, fail(422, "duplicate_id", field+"/evidence_id", "候选内证据 ID 不能重复")
			}
			evidenceIDs[e.ID] = true
			if !publicURL(e.URL) {
				return b, fail(422, "invalid_source_url", field+"/url", "只接受无凭据的公开 HTTP(S) 来源")
			}
		}
		for j, a := range c.Alternatives {
			if !publicURL(a.URL) {
				return b, fail(422, "invalid_source_url", fmt.Sprintf("%s/alternatives/%d/url", path, j), "只接受无凭据的公开 HTTP(S) 来源")
			}
		}
	}
	seen = map[string]bool{}
	for i, u := range b.RunReport.Usage {
		if seen[u.ID] {
			return b, fail(422, "duplicate_id", fmt.Sprintf("/run_report/usage/%d/usage_id", i), "批内费用 ID 不能重复")
		}
		seen[u.ID] = true
	}
	return b, nil
}

func publicURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || netip.MustParsePrefix("100.64.0.0/10").Contains(ip) {
			return false
		}
	}
	return true
}

func (s *Service) Receive(p auth.Producer, raw []byte) ([]byte, error) {
	return s.receive(p, raw, false)
}

// ReceiveReport 是生产入口；Receive 仅用于旧资料处理及历史回归。
func (s *Service) ReceiveReport(p auth.Producer, raw []byte) ([]byte, error) {
	return s.receive(p, raw, true)
}

func (s *Service) receive(p auth.Producer, raw []byte, externalOnly bool) ([]byte, error) {
	b, err := s.parse(raw)
	if err != nil {
		return nil, err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = verifyProducer(tx, p); err != nil {
		return nil, err
	}
	var previousHash string
	var reception []byte
	err = tx.QueryRow("SELECT payload_hash,reception FROM research_batches WHERE producer_id=? AND batch_id=?", p.ID, b.BatchID).Scan(&previousHash, &reception)
	if err == nil {
		if previousHash != digest {
			return nil, fail(409, "batch_conflict", "/batch_id", "相同批次 ID 的请求字节不同")
		}
		return reception, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	if externalOnly {
		if len(b.Candidates) != 0 {
			return nil, fail(410, "candidate_intake_retired", "/candidates", "站内候选核验已停用；请由外部 Codex 判断后提交立项包")
		}
		if err = recomputeTasks(tx, time.Now()); err != nil {
			return nil, err
		}
	}
	task, err := scanTask(tx.QueryRow("SELECT "+taskColumns+" FROM research_tasks WHERE id=? AND producer_id=?", b.TaskID, p.ID))
	if err == sql.ErrNoRows {
		return nil, fail(404, "task_not_found", "/task_id", "任务不存在或未分配给该执行端")
	}
	if err != nil {
		return nil, err
	}
	if task.State == "cancelled" || !task.Current {
		return nil, fail(409, "task_cancelled", "/task_id", "任务已取消或被替代")
	}
	if externalOnly && (!projectTask(task) || task.State != "active") {
		return nil, fail(409, "intake_not_active", "/task_id", "仅活动的外部立项接收会话允许提交新报告")
	}
	if projectTask(task) && len(b.Candidates) > 0 {
		return nil, fail(409, "project_package_required", "/schema_version", "新模式只接收立项包，未通过候选保留在外部工件")
	}
	if task.RulesVersion != b.RulesVersion {
		return nil, fail(409, "rules_changed", "/rules_version", "请读取当前任务规则并重新整理新批次")
	}
	status := "pending_validation"
	if externalOnly {
		status = "completed"
	}
	reception, err = json.Marshal(map[string]any{"batch_id": b.BatchID, "status": status, "receipt_url": "/api/research/v1/batches/" + b.BatchID})
	if err != nil {
		return nil, err
	}
	result, err := tx.Exec("INSERT INTO research_batches(producer_id,batch_id,task_id,rules_version,run_id,payload,payload_hash,reception,status) VALUES (?,?,?,?,?,?,?,?,?)", p.ID, b.BatchID, b.TaskID, b.RulesVersion, b.RunID, raw, digest, reception, status)
	if err != nil {
		return nil, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	for i, c := range b.Candidates {
		if _, err = tx.Exec("INSERT INTO research_candidates(batch_row,external_id,title,ordinal) VALUES (?,?,?,?)", id, c.ExternalID, c.Title, i); err != nil {
			return nil, err
		}
	}
	for _, c := range b.Candidates {
		var locked int
		if err = tx.QueryRow("SELECT count(*) FROM project_imports i JOIN need_aliases a ON a.need_id=i.need_id WHERE a.producer_id=? AND a.external_id=?", p.ID, c.ExternalID).Scan(&locked); err != nil {
			return nil, err
		}
		if locked > 0 {
			return nil, fail(409, "project_package_required", "/candidates", "已立项项目只能通过新的外部复核包更新")
		}
		if _, err = tx.Exec("UPDATE needs SET status='pending',valid_until=0,version=version+1 WHERE id=(SELECT need_id FROM need_aliases WHERE producer_id=? AND external_id=?) AND status!='completed'", p.ID, c.ExternalID); err != nil {
			return nil, err
		}
		if _, err = tx.Exec("INSERT INTO need_aliases(producer_id,external_id,latest_batch) VALUES (?,?,?) ON CONFLICT(producer_id,external_id) DO UPDATE SET latest_batch=excluded.latest_batch", p.ID, c.ExternalID, id); err != nil {
			return nil, err
		}
	}
	if err = usage.RecordExternal(tx, p.ID, time.Now().Unix(), b.RunReport.Usage); err != nil {
		return nil, err
	}
	if externalOnly && b.RunReport.State != "submitted" {
		if _, err = tx.Exec("UPDATE research_tasks SET state=?,version=version+1,updated_at=unixepoch() WHERE id=?", b.RunReport.State, task.ID); err != nil {
			return nil, err
		}
	}
	if err = recomputeTasks(tx, time.Now()); err != nil {
		return nil, err
	}
	return reception, tx.Commit()
}

type Result struct {
	ExternalID      string   `json:"external_id"`
	NeedID          *int     `json:"need_id"`
	Outcome         *string  `json:"outcome"`
	ValidationState string   `json:"validation_state"`
	ReasonCodes     []string `json:"reason_codes"`
	Issues          []string `json:"issues"`
	Suggestions     []string `json:"suggestions"`
}
type Receipt struct {
	BatchID        string   `json:"batch_id"`
	Status         string   `json:"status"`
	Results        []Result `json:"results"`
	QualifiedCount int      `json:"qualified_count"`
	TargetCount    int      `json:"target_count"`
	RemainingGap   int      `json:"remaining_gap"`
	TaskState      string   `json:"task_state"`
	ShouldContinue bool     `json:"should_continue"`
}

func (s *Service) Receipt(producer auth.Producer, batchID string) (Receipt, error) {
	var receipt Receipt
	var taskID string
	var rowID int
	tx, err := s.db.Begin()
	if err != nil {
		return receipt, err
	}
	defer tx.Rollback()
	if err = verifyProducer(tx, producer); err != nil {
		return receipt, err
	}
	err = tx.QueryRow("SELECT id,batch_id,status,task_id FROM research_batches WHERE producer_id=? AND batch_id=?", producer.ID, batchID).Scan(&rowID, &receipt.BatchID, &receipt.Status, &taskID)
	if err == sql.ErrNoRows {
		return receipt, fail(404, "batch_not_found", "/batch_id", "回执不存在")
	}
	if err != nil {
		return receipt, err
	}
	if err = recomputeTasks(tx, time.Now()); err != nil {
		return receipt, err
	}
	task, err := scanTask(tx.QueryRow("SELECT "+taskColumns+" FROM research_tasks WHERE id=?", taskID))
	if err != nil {
		return receipt, err
	}
	if err = hydrateTask(tx, task, time.Now()); err != nil {
		return receipt, err
	}
	receipt.TargetCount = task.TargetCount
	receipt.QualifiedCount = task.QualifiedCount
	receipt.RemainingGap = task.RemainingGap
	receipt.TaskState = task.State
	receipt.ShouldContinue = task.ShouldContinue
	receipt.Results = []Result{}
	rows, err := tx.Query("SELECT external_id,result FROM research_candidates WHERE batch_row=? ORDER BY ordinal", rowID)
	if err != nil {
		return receipt, err
	}
	for rows.Next() {
		r := Result{ValidationState: "pending_validation", ReasonCodes: []string{}, Issues: []string{}, Suggestions: []string{"旧核验已停用；后续研究与立项判断由外部 Codex 完成，接收成功不代表证据合格"}}
		var stored *string
		if err = rows.Scan(&r.ExternalID, &stored); err != nil {
			rows.Close()
			return receipt, err
		}
		if stored != nil {
			if err = json.Unmarshal([]byte(*stored), &r); err != nil {
				rows.Close()
				return receipt, err
			}
		}
		receipt.Results = append(receipt.Results, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return receipt, err
	}
	return receipt, tx.Commit()
}

// 凭据版本与受保护数据共用事务，避免撤销后读到新产生的资料。
func verifyProducer(tx *sql.Tx, p auth.Producer) error {
	var enabled int
	if err := tx.QueryRow("SELECT count(*) FROM producers WHERE id=? AND version=? AND token_hash IS NOT NULL", p.ID, p.Version).Scan(&enabled); err != nil {
		return err
	}
	if enabled != 1 {
		return fail(401, "invalid_token", "/", "令牌已撤销或轮换")
	}
	return nil
}

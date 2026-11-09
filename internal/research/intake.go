package research

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yyl1212/creative/internal/auth"
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
	if !utf8.Valid(raw) {
		return b, fail(400, "invalid_json", "/", "JSON 必须为有效 UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := uniqueJSON(dec, 0); err != nil {
		return b, fail(400, "invalid_json", "/", "JSON 无效、重复字段或嵌套过深")
	}
	if _, err := dec.Token(); err != io.EOF {
		return b, fail(400, "invalid_json", "/", "请求只能包含一个 JSON 值")
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

// JSON 的重复键会被 encoding/json 静默覆盖，须在 Schema 校验前拒绝。
func uniqueJSON(d *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("depth")
	}
	tok, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return errors.New("duplicate key")
			}
			seen[s] = true
			if err = uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err = uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("delimiter")
	}
	_, err = d.Token()
	return err
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
	if task.RulesVersion != b.RulesVersion {
		return nil, fail(409, "rules_changed", "/rules_version", "请读取当前任务规则并重新整理新批次")
	}
	reception, err = json.Marshal(map[string]any{"batch_id": b.BatchID, "status": "pending_validation", "receipt_url": "/api/research/v1/batches/" + b.BatchID})
	if err != nil {
		return nil, err
	}
	result, err := tx.Exec("INSERT INTO research_batches(producer_id,batch_id,task_id,rules_version,run_id,payload,payload_hash,reception) VALUES (?,?,?,?,?,?,?,?)", p.ID, b.BatchID, b.TaskID, b.RulesVersion, b.RunID, raw, digest, reception)
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
		if _, err = tx.Exec("UPDATE needs SET status='pending',valid_until=0,version=version+1 WHERE id=(SELECT need_id FROM need_aliases WHERE producer_id=? AND external_id=?) AND status!='completed'", p.ID, c.ExternalID); err != nil {
			return nil, err
		}
		if _, err = tx.Exec("INSERT INTO need_aliases(producer_id,external_id,latest_batch) VALUES (?,?,?) ON CONFLICT(producer_id,external_id) DO UPDATE SET latest_batch=excluded.latest_batch", p.ID, c.ExternalID, id); err != nil {
			return nil, err
		}
	}
	if err = recordExternalUsage(tx, p.ID, b.RunReport.Usage); err != nil {
		return nil, err
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
		r := Result{ValidationState: "pending_validation", ReasonCodes: []string{}, Issues: []string{}, Suggestions: []string{"等待网站原文核验；接收成功不代表证据合格"}}
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

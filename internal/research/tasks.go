package research

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/yyl1212/creative/internal/auth"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const rulesVersion = "strong-wtp.v1"
const rulesJSON = `{"independent_users_min":3,"original_sites_min":2,"direct_payment_users_min":2,"specific_price_or_locatable_spend_min":1,"payment_evidence_max_age_months":12,"qualification_valid_days":30,"requirements":["同一明确核心任务，可自助交付","原始公开链接、可定位引用、来源内作者、发布时间与抓取时间","重复作者、转载、推广和无法确认独立性的材料不计数","付费条件及明确未满足点须有原文支持，不能以竞品报价替代用户证据","金额、币种、周期未知保持 null；否定、讽刺和条件表态不能误判","网站独立核验，合格不等于立项或允许开发"]}`

type Task struct {
	ID              string          `json:"task_id"`
	ProducerID      string          `json:"-"`
	ProducerName    string          `json:"-"`
	Topics          []string        `json:"topics"`
	Languages       []string        `json:"languages"`
	TargetCount     int             `json:"target_count"`
	QualifiedCount  int             `json:"qualified_count"`
	RemainingGap    int             `json:"remaining_gap"`
	RulesVersion    string          `json:"rules_version"`
	Rules           json.RawMessage `json:"rules"`
	State           string          `json:"state"`
	ShouldContinue  bool            `json:"should_continue"`
	ResumeCondition string          `json:"resume_condition"`
	UpdatedAt       string          `json:"updated_at"`
	Version         int             `json:"version"`
	Current         bool            `json:"-"`
}

const taskColumns = "id,producer_id,topics,languages,target_count,rules_version,rules,state,is_current,version,updated_at,(SELECT p.name FROM producers p WHERE p.id=research_tasks.producer_id)"

func scanTask(row interface{ Scan(...any) error }) (*Task, error) {
	var t Task
	var topics, languages, rules string
	var updated int64
	if err := row.Scan(&t.ID, &t.ProducerID, &topics, &languages, &t.TargetCount, &t.RulesVersion, &rules, &t.State, &t.Current, &t.Version, &updated, &t.ProducerName); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(topics), &t.Topics); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(languages), &t.Languages); err != nil {
		return nil, err
	}
	t.Rules = json.RawMessage(rules)
	t.RemainingGap = t.TargetCount
	// shortcut: 迭代 2 尚无核验结论；迭代 3 在此统一重算去重且未过期的资格。
	t.ShouldContinue = t.Current && t.State == "active"
	t.UpdatedAt = time.Unix(updated, 0).UTC().Format(time.RFC3339)
	switch t.State {
	case "cancelled":
		t.ResumeCondition = "仅管理员明确恢复当前任务"
	case "paused_service":
		t.ResumeCondition = "服务恢复并由管理员恢复"
	case "waiting_for_signals":
		t.ResumeCondition = "获得新线索后由管理员恢复"
	}
	return &t, nil
}
func (s *Service) CurrentTask(producer auth.Producer) (*Task, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = verifyProducer(tx, producer); err != nil {
		return nil, err
	}
	t, err := scanTask(tx.QueryRow("SELECT "+taskColumns+" FROM research_tasks WHERE producer_id=? AND is_current=1", producer.ID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t, tx.Commit()
}
func (s *Service) Tasks() ([]Task, error) {
	rows, err := s.db.Query("SELECT " + taskColumns + " FROM research_tasks WHERE is_current=1 ORDER BY created_at DESC,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := []Task{}
	for rows.Next() {
		t, e := scanTask(rows)
		if e != nil {
			return nil, e
		}
		tasks = append(tasks, *t)
	}
	return tasks, rows.Err()
}
func (s *Service) CreateTask(producer string, topics, languages []string, target int, actor, source string) (*Task, error) {
	if target < 1 || target > 2147483647 || len(topics) == 0 || len(topics) > 20 || len(languages) == 0 || len(languages) > 2 {
		return nil, fail(422, "invalid_task", "/", "请填写主题、语言和正整数目标")
	}
	for i, topic := range topics {
		topics[i] = strings.TrimSpace(topic)
		if topics[i] == "" || utf8.RuneCountInString(topics[i]) > 160 || strings.IndexFunc(topics[i], unicode.IsControl) >= 0 {
			return nil, fail(422, "invalid_task", "/topics", "主题格式无效")
		}
	}
	for i, lang := range languages {
		if lang != "zh" && lang != "en" || i > 0 && languages[i-1] == lang {
			return nil, fail(422, "invalid_task", "/languages", "语言只支持 zh/en，且不能重复")
		}
	}
	topicJSON, _ := json.Marshal(topics)
	languageJSON, _ := json.Marshal(languages)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var enabled int
	if err = tx.QueryRow("SELECT count(*) FROM producers WHERE id=? AND token_hash IS NOT NULL", producer).Scan(&enabled); err != nil {
		return nil, err
	}
	if enabled == 0 {
		return nil, fail(422, "invalid_producer", "/producer_id", "请选择有效执行端")
	}
	var currentState string
	err = tx.QueryRow("SELECT state FROM research_tasks WHERE producer_id=? AND is_current=1", producer).Scan(&currentState)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err == nil && currentState != "cancelled" {
		return nil, fail(409, "current_task_exists", "/task_id", "请先取消当前任务")
	}
	if _, err = tx.Exec("UPDATE research_tasks SET is_current=0,version=version+1 WHERE producer_id=? AND is_current=1", producer); err != nil {
		return nil, err
	}
	id := "task-" + rand.Text()
	if _, err = tx.Exec("INSERT INTO research_tasks(id,producer_id,topics,languages,target_count,rules_version,rules,state) VALUES (?,?,?,?,?,?,?,'active')", id, producer, string(topicJSON), string(languageJSON), target, rulesVersion, rulesJSON); err != nil {
		return nil, err
	}
	if _, err = tx.Exec("INSERT INTO audit_events(action,account,source,object_id,details) VALUES ('task_created',?,?,?,?)", actor, source, id, `{"before":null,"after":"active","version":1}`); err != nil {
		return nil, err
	}
	task, err := scanTask(tx.QueryRow("SELECT "+taskColumns+" FROM research_tasks WHERE id=?", id))
	if err != nil {
		return nil, err
	}
	return task, tx.Commit()
}
func (s *Service) ChangeTask(id string, version int, action, actor, source string) (*Task, error) {
	if action != "cancel" && action != "resume" {
		return nil, fail(422, "invalid_action", "/action", "只允许取消或恢复")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	task, err := scanTask(tx.QueryRow("SELECT "+taskColumns+" FROM research_tasks WHERE id=?", id))
	if err == sql.ErrNoRows {
		return nil, fail(404, "task_not_found", "/task_id", "任务不存在")
	}
	if err != nil {
		return nil, err
	}
	if task.Version != version || !task.Current {
		return nil, fail(409, "task_changed", "/version", "任务已变更，请刷新")
	}
	state := "cancelled"
	if action == "resume" {
		state = "active"
		if task.State == "active" || task.State == "target_reached" {
			return nil, fail(409, "task_not_paused", "/task_id", "任务不需要恢复")
		}
	}
	if _, err = tx.Exec("UPDATE research_tasks SET state=?,version=version+1,updated_at=unixepoch() WHERE id=? AND version=?", state, id, version); err != nil {
		return nil, err
	}
	detail := fmt.Sprintf(`{"before":%q,"after":%q,"version":%d}`, task.State, state, version+1)
	if _, err = tx.Exec("INSERT INTO audit_events(action,account,source,object_id,details) VALUES (?,?,?,?,?)", "task_"+action, actor, source, id, detail); err != nil {
		return nil, err
	}
	task, err = scanTask(tx.QueryRow("SELECT "+taskColumns+" FROM research_tasks WHERE id=?", id))
	if err != nil {
		return nil, err
	}
	return task, tx.Commit()
}

package research

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	"github.com/yyl1212/creative/internal/release"
)

// VerifyRelease 只由受控 CLI 调用。网络核验结束后在事务中再次检查开发版本与证据。
func (s *Service) VerifyRelease(ctx context.Context, id, version int, dist, catalog string) error {
	d, e := s.Development(id)
	if e != nil {
		return e
	}
	if d.Version != version || d.State == "unverified" {
		return fail(409, "release_not_ready", "/state", "记录版本已变化或恢复批准未重新确认")
	}
	m, e := release.ReadManifest(filepath.Join(dist, ".well-known", "creative-release.json"))
	if e != nil {
		return e
	}
	observed := release.Verify(ctx, dist, catalog, m, nil)
	return s.recordRelease(id, version, m, observed)
}

func (s *Service) recordRelease(id, version int, m release.Manifest, observed error) error {
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = lockDevelopment(tx, id); e != nil {
		return e
	}
	d, e := scanDevelopment(tx.QueryRow("SELECT "+developmentColumns+" FROM development_records d JOIN needs n ON n.id=d.need_id WHERE d.need_id=?", id))
	if e == sql.ErrNoRows {
		return fail(404, "development_not_found", "/id", "开发记录不存在")
	}
	if e != nil {
		return e
	}
	if d.Version != version {
		return fail(409, "development_version_conflict", "/version", "开发记录已变化，请重新核验")
	}
	if d.State != "review_ready" && d.State != "launched" && d.State != "failed" && d.State != "rolled_back" {
		return fail(409, "release_not_ready", "/state", "尚未取得完整开发交付")
	}
	raw := jsonText(m)
	if len(raw) > 4<<20 || secretPattern.MatchString(raw) {
		return lifecycleInput("发布标记过大或包含疑似凭据")
	}
	var slug string
	for _, p := range m.Products {
		if p.NeedID == id {
			slug = p.Slug
		}
	}
	if m.Schema != "creative-release/v1" || m.Environment != "production" || m.Commit != d.Result.Commit || m.Origin != d.Design.ProductionOrigin || slug == "" || slug != d.Design.ProductSlug || !d.Result.ChecksPassed || d.Result.ReviewCommit != m.Commit || d.Result.TestCommit != m.Commit || d.Result.RemainingIssues != "" {
		observed = fail(409, "release_mismatch", "/release", "生产、产品清单、提交或审查记录不匹配")
	}
	_, evidenceErr := currentEvidence(tx, id, d.NeedVersion, d.VerificationID, true)
	if evidenceErr != nil {
		observed = evidenceErr
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))
	var active int
	if e = tx.QueryRow("SELECT count(*) FROM release_records WHERE need_id=? AND manifest_hash=? AND state='verified'", id, digest).Scan(&active); e != nil {
		return e
	}
	if observed == nil && active == 1 && d.State == "launched" {
		return tx.Commit()
	}
	releaseState, developmentState, action, reason := "verified", "launched", "release_verified", ""
	if observed != nil {
		releaseState, developmentState, action, reason = "failed", "failed", "release_failed", "生产或资格核验未通过；修复后重新核验"
	}
	if _, e = tx.Exec("UPDATE release_records SET state='unverified' WHERE need_id=? AND state='verified'", id); e != nil {
		return e
	}
	if _, e = tx.Exec(`INSERT INTO release_records(need_id,record_version,commit_sha,slug,origin,manifest_hash,manifest,state,verified_at,error) VALUES (?,?,?,?,?,?,?,?,unixepoch(),?) ON CONFLICT(need_id,manifest_hash) DO UPDATE SET record_version=excluded.record_version,state=excluded.state,verified_at=excluded.verified_at,error=excluded.error`, id, version+1, m.Commit, slug, m.Origin, digest, raw, releaseState, reason); e != nil {
		return e
	}
	if observed == nil {
		_, e = tx.Exec("UPDATE needs SET status='completed',version=version+1 WHERE id=?", id)
	} else if evidenceErr == nil {
		_, e = tx.Exec("UPDATE needs SET status='qualified',version=version+1 WHERE id=?", id)
	} else {
		_, e = tx.Exec("UPDATE needs SET status='pending',version=version+1 WHERE id=? AND status='completed'", id)
	}
	if e != nil {
		return e
	}
	if evidenceErr == nil {
		d.NeedVersion++
	}
	if _, e = tx.Exec("UPDATE development_records SET state=?,version=version+1,need_version=?,updated_at=unixepoch() WHERE need_id=?", developmentState, d.NeedVersion, id); e != nil {
		return e
	}
	if e = lifecycleAudit(tx, action, id, version+1, "controlled-cli", "local"); e != nil {
		return e
	}
	if e = recomputeTasks(tx, time.Now()); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	if observed != nil {
		return fail(409, "release_verification_failed", "/release", reason)
	}
	return nil
}

// RollbackRelease 登记已执行的运维回滚，撤销完成计数；不会执行部署。
func (s *Service) RollbackRelease(id, version int, reason, actor string) error {
	if !safeText(reason, true) {
		return lifecycleInput("请填写不含凭据的回滚依据")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = lockDevelopment(tx, id); e != nil {
		return e
	}
	var current, needVersion, verification int
	var state string
	if e = tx.QueryRow("SELECT version,state,need_version,verification_id FROM development_records WHERE need_id=?", id).Scan(&current, &state, &needVersion, &verification); e == sql.ErrNoRows {
		return fail(404, "development_not_found", "/id", "开发记录不存在")
	}
	if e != nil {
		return e
	}
	if current != version {
		return fail(409, "development_version_conflict", "/version", "开发记录已变化")
	}
	if state != "launched" && state != "failed" && state != "unverified" {
		return fail(409, "release_not_launched", "/state", "没有可撤销的发布记录")
	}
	if _, e = tx.Exec("UPDATE release_records SET state='rolled_back',error=? WHERE need_id=? AND state IN ('verified','failed','unverified')", reason, id); e != nil {
		return e
	}
	if _, gate := currentEvidence(tx, id, needVersion, verification, true); gate == nil {
		_, e = tx.Exec("UPDATE needs SET status='qualified',version=version+1 WHERE id=?", id)
		needVersion++
	} else {
		_, e = tx.Exec("UPDATE needs SET status='pending',version=version+1 WHERE id=? AND status='completed'", id)
	}
	if e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE development_records SET state='rolled_back',version=version+1,need_version=?,updated_at=unixepoch() WHERE need_id=?", needVersion, id); e != nil {
		return e
	}
	if e = lifecycleAudit(tx, "release_rolled_back", id, version+1, actor, "local"); e != nil {
		return e
	}
	if e = recomputeTasks(tx, time.Now()); e != nil {
		return e
	}
	return tx.Commit()
}

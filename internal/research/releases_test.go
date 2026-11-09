package research

import (
	"errors"
	"strings"
	"testing"

	"github.com/yyl1212/creative/internal/release"
)

func releaseFixture(t *testing.T) (*Service, int, release.Manifest) {
	t.Helper()
	s, _, n, a := approvedFixture(t)
	if e := s.ApproveNeed(1, n.Version, a, "owner", "local"); e != nil {
		t.Fatal(e)
	}
	d, _ := s.Development(1)
	if e := s.ConfirmDesign(1, d.Version, designFixture(), "owner", "local"); e != nil {
		t.Fatal(e)
	}
	d, _ = s.Development(1)
	if e := s.StartDevelopment(1, d.Version, "owner", "local"); e != nil {
		t.Fatal(e)
	}
	d, _ = s.Development(1)
	commit := strings.Repeat("b", 40)
	result := DevelopmentResult{Commit: commit, PullRequest: "https://github.com/yyl1212/creative/pull/10", Summary: "按规格完成", ReviewCommit: commit, TestCommit: commit, ReviewReport: "docs/review.md", TestReport: "docs/tests.md", ChecksPassed: true}
	if e := s.RecordDevelopment(1, d.Version, result, "owner", "local"); e != nil {
		t.Fatal(e)
	}
	d, _ = s.Development(1)
	return s, d.Version, release.Manifest{Schema: "creative-release/v1", Commit: commit, Origin: "https://example.org", Environment: "production", Products: []release.Product{{NeedID: 1, Slug: "example"}}, Files: map[string]string{"en/tools/example/index.html": strings.Repeat("a", 64), "zh/tools/example/index.html": strings.Repeat("b", 64)}}
}
func TestReleaseVerification(t *testing.T) {
	for _, scenario := range []string{"valid_duplicate_rollback", "commit", "origin", "catalog", "preview", "failed", "expired", "version", "rollback_failed_verification"} {
		t.Run(scenario, func(t *testing.T) {
			s, v, m := releaseFixture(t)
			var observed error
			switch scenario {
			case "commit":
				m.Commit = strings.Repeat("c", 40)
			case "origin":
				m.Origin = "https://wrong.example.org"
			case "catalog":
				m.Products[0].Slug = "wrong"
			case "preview":
				m.Environment = "preview"
			case "failed":
				observed = errors.New("network unavailable")
			case "expired":
				s.db.Exec("UPDATE needs SET valid_until=1 WHERE id=1")
			case "version":
				v--
			}
			e := s.recordRelease(1, v, m, observed)
			if scenario != "valid_duplicate_rollback" && scenario != "rollback_failed_verification" {
				if e == nil {
					t.Fatal("invalid release accepted")
				}
				_, _, completed, _ := s.Counts()
				if completed != 0 {
					t.Fatal("failure counted completed")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			d, _ := s.Development(1)
			if d.State != "launched" {
				t.Fatal(d.State)
			}
			_, _, completed, _ := s.Counts()
			if completed != 1 {
				t.Fatal("not completed")
			}
			if scenario == "rollback_failed_verification" {
				if e = s.recordRelease(1, d.Version, m, errors.New("production changed")); e == nil {
					t.Fatal("failed recheck accepted")
				}
			} else {
				if e = s.recordRelease(1, d.Version, m, nil); e != nil {
					t.Fatal(e)
				}
				again, _ := s.Development(1)
				if again.Version != d.Version {
					t.Fatal("duplicate increments version")
				}
				if e = s.RollbackRelease(1, d.Version, "已恢复上一生产版本", "owner"); e != nil {
					t.Fatal(e)
				}
			}
			_, _, completed, _ = s.Counts()
			if completed != 0 {
				t.Fatal("rollback or failure retained completion")
			}
		})
	}
}

func TestReleaseSyncFailureIsAtomic(t *testing.T) {
	s, v, m := releaseFixture(t)
	if _, e := s.db.Exec("CREATE TRIGGER release_audit_failure BEFORE INSERT ON audit_events WHEN NEW.action='release_verified' BEGIN SELECT RAISE(ABORT,'fixture failure'); END"); e != nil {
		t.Fatal(e)
	}
	if e := s.recordRelease(1, v, m, nil); e == nil {
		t.Fatal("sync failure accepted")
	}
	d, e := s.Development(1)
	if e != nil || d.State != "review_ready" || d.Version != v {
		t.Fatal("partial development update", d, e)
	}
	var count int
	if e = s.db.QueryRow("SELECT count(*) FROM release_records").Scan(&count); e != nil || count != 0 {
		t.Fatal("partial release update", count, e)
	}
	_, _, completed, e := s.Counts()
	if e != nil || completed != 0 {
		t.Fatal("partial completion", completed, e)
	}
}

func TestReleaseFailureCannotRequalifySupplement(t *testing.T) {
	s, v, m := releaseFixture(t)
	if _, e := s.db.Exec("UPDATE needs SET status='pending',version=version+1 WHERE id=1"); e != nil {
		t.Fatal(e)
	}
	if e := s.recordRelease(1, v, m, nil); e == nil {
		t.Fatal("stale evidence released")
	}
	n, e := s.Need(1, 0)
	if e != nil || n.Status != "pending" {
		t.Fatal("pending evidence qualified", n, e)
	}
	d, _ := s.Development(1)
	if e = s.RollbackRelease(1, d.Version, "隔离回滚登记", "owner"); e != nil {
		t.Fatal(e)
	}
	n, e = s.Need(1, 0)
	if e != nil || n.Status != "pending" {
		t.Fatal("rollback qualified pending evidence", n, e)
	}
	if _, e = s.TaskBundle(1, "development"); e == nil {
		t.Fatal("stale approval rebound")
	}
}

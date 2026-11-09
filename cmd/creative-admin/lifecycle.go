package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/yyl1212/creative/internal/release"
	"github.com/yyl1212/creative/internal/research"
	"github.com/yyl1212/creative/internal/store"
)

func lifecycle(command string, args []string) error {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	path := flags.String("db", defaultDB(), "私有数据库")
	directory := flags.String("directory", "", "备份目录")
	source := flags.String("source", "", "备份文件")
	destination := flags.String("destination", "", "全新恢复路径")
	need := flags.Int("need", 0, "需求 ID")
	version := flags.Int("version", 0, "页面显示的开发记录版本")
	repo := flags.String("repo", ".", "已批准的 Git 工作区")
	dist := flags.String("dist", "dist", "公开构建目录")
	catalog := flags.String("catalog", "src/data/products.json", "公开产品清单")
	reason := flags.String("reason", "", "回滚依据，不填凭据")
	date := flags.String("date", "", "上海日期 YYYY-MM-DD，默认今天")
	if e := flags.Parse(args); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return errors.New("不支持额外位置参数")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()
	if command == "restore" {
		if *source == "" || *destination == "" {
			return errors.New("恢复需指定 --source 与 --destination")
		}
		if e := store.RestoreBackup(ctx, *source, *destination); e != nil {
			return e
		}
		fmt.Println("已恢复到新路径；管理员、会话、机器令牌和发布状态已失效，搜寻与模型保持禁用")
		return nil
	}
	// 运维命令拒绝拼错路径时创建空库；新库使用 migrate 显式创建。
	if info, e := os.Stat(*path); e != nil || !info.Mode().IsRegular() {
		return errors.New("数据库不存在或不是普通文件；新库请先 migrate")
	}
	db, e := store.Open(*path)
	if e != nil {
		return e
	}
	defer db.Close()
	if command == "backup" {
		if *directory == "" {
			return errors.New("备份需指定 --directory")
		}
		file, e := store.Backup(ctx, db, *directory, time.Now())
		if e != nil {
			return e
		}
		fmt.Println(file)
		return nil
	}
	s, e := research.New(db)
	if e != nil {
		return e
	}
	if command == "report" {
		r, e := s.DailyReport(*date)
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(r)
	}
	if *need < 1 || *version < 1 {
		return errors.New("需要有效 --need 与 --version")
	}
	if command == "release-rollback" {
		return s.RollbackRelease(*need, *version, *reason, "controlled-cli")
	}
	d, e := s.Development(*need)
	if e != nil {
		return e
	}
	if d.Version != *version {
		return errors.New("开发版本已变化，请刷新后重试")
	}
	if e = checkGit(ctx, *repo, *d); e != nil {
		return e
	}
	if command == "release-prepare" {
		if d.State != "review_ready" && d.State != "failed" && d.State != "rolled_back" {
			return errors.New("当前状态不能准备发布；先完成审查与回归")
		}
		if _, e = s.TaskBundle(*need, "development"); e != nil {
			return e
		}
		m, e := release.Prepare(*dist, *catalog, d.Result.Commit, d.Design.ProductionOrigin, "production")
		if e != nil {
			return e
		}
		found := false
		for _, p := range m.Products {
			if p.NeedID == *need && p.Slug == d.Design.ProductSlug {
				found = true
			}
		}
		if !found {
			return errors.New("产品清单未包含批准的需求及 slug")
		}
		fmt.Println("已生成生产构建标记；尚未部署、尚未增加完成数量")
		return nil
	}
	if command == "release-verify" {
		m, e := release.ReadManifest(filepath.Join(*dist, ".well-known", "creative-release.json"))
		if e != nil {
			return e
		}
		if m.Commit != d.Result.Commit || m.Origin != d.Design.ProductionOrigin {
			return errors.New("发布标记不属于批准的提交或生产域名")
		}
		if e = s.VerifyRelease(ctx, *need, *version, *dist, *catalog); e != nil {
			return e
		}
		fmt.Println("生产内容核验通过，已同步完成状态")
		return nil
	}
	return errors.New("未知运维命令")
}

func defaultDB() string {
	if value := os.Getenv("CREATIVE_DB"); value != "" {
		return value
	}
	return "data/creative.db"
}
func checkGit(ctx context.Context, repo string, d research.Development) error {
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
		out, e := cmd.Output()
		if e != nil {
			return "", errors.New("Git 核验失败，请检查批准工作区")
		}
		return strings.TrimSpace(string(out)), nil
	}
	head, e := git("rev-parse", "HEAD")
	if e != nil {
		return e
	}
	branch, e := git("branch", "--show-current")
	if e != nil {
		return e
	}
	remote, e := git("remote", "get-url", "origin")
	if e != nil {
		return e
	}
	remote = strings.TrimSuffix(strings.Replace(remote, "git@github.com:", "https://github.com/", 1), ".git")
	dirty, e := git("status", "--porcelain")
	if e != nil {
		return e
	}
	if head != d.Result.Commit || head != d.Result.ReviewCommit || head != d.Result.TestCommit || branch != d.Design.Branch || remote != d.Design.Repository || dirty != "" {
		return errors.New("工作区必须干净，且仓库、分支、HEAD、审查与回归提交一致")
	}
	if _, e = git("merge-base", "--is-ancestor", d.Design.Baseline, head); e != nil {
		return errors.New("批准的 master 基线不是当前提交祖先")
	}
	for _, file := range []string{d.Design.Spec, d.Design.Architecture, d.Design.Plan, d.Result.ReviewReport, d.Result.TestReport} {
		if file == "" {
			return errors.New("缺少设计或验收文件")
		}
		if _, e = git("cat-file", "-e", head+":"+file); e != nil {
			return errors.New("批准的设计或验收文件未包含在当前提交")
		}
	}
	return nil
}

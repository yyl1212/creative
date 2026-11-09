package main

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"strings"

	"github.com/yyl1212/creative/internal/research"
	"github.com/yyl1212/creative/internal/store"
)

func intake(command string, args []string) error {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	path := flags.String("db", defaultDB(), "已有私有数据库")
	producer := flags.String("producer", "", "外部智能体 ID")
	topic := flags.String("topic", "", "接收主题")
	languages := flags.String("languages", "zh,en", "语言，以逗号分隔")
	target := flags.Int("target", 0, "外部已立项项目目标数")
	id := flags.String("task", "", "接收会话 ID")
	version := flags.Int("version", 0, "接收会话当前版本")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("不支持额外位置参数")
	}
	if info, err := os.Stat(*path); err != nil || !info.Mode().IsRegular() {
		return errors.New("数据库不存在或不是普通文件；新库请先 migrate")
	}
	db, err := store.Open(*path)
	if err != nil {
		return err
	}
	defer db.Close()
	s, err := research.New(db)
	if err != nil {
		return err
	}
	var task *research.Task
	if command == "intake-create" {
		task, err = s.CreateProjectTask(*producer, []string{*topic}, strings.Split(*languages, ","), *target, "controlled-cli", "external-agent")
	} else {
		if *id == "" || *version < 1 {
			return errors.New("需要 --task 与有效 --version")
		}
		if command != "intake-cancel" && command != "intake-resume" {
			return errors.New("未知接收命令")
		}
		if command == "intake-resume" {
			tasks, e := s.Tasks()
			if e != nil {
				return e
			}
			for _, item := range tasks {
				if item.ID != *id {
					continue
				}
				var rules struct {
					Mode string `json:"decision_mode"`
				}
				if e = json.Unmarshal(item.Rules, &rules); e != nil {
					return e
				}
				if rules.Mode != "agent_project" {
					return errors.New("旧研究模式不能恢复；请取消旧会话后登记新的外部立项接收会话")
				}
			}
		}
		task, err = s.ChangeTask(*id, *version, strings.TrimPrefix(command, "intake-"), "controlled-cli", "external-agent")
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(task)
}

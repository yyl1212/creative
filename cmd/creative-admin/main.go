package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"github.com/yyl1212/creative/internal/auth"
	"github.com/yyl1212/creative/internal/store"
	"golang.org/x/term"
	"io"
	"os"
	"strings"
)

func password(stdin bool) (string, error) {
	if stdin {
		contents, err := io.ReadAll(io.LimitReader(os.Stdin, 131))
		if err != nil {
			return "", err
		}
		if len(contents) > 130 {
			return "", errors.New("密码输入过长")
		}
		return strings.TrimSuffix(strings.TrimSuffix(string(contents), "\n"), "\r"), nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("需要交互终端，或显式使用 --password-stdin")
	}
	fmt.Fprint(os.Stderr, "密码（不回显）：")
	first, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "再次输入：")
	second, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(first) != string(second) {
		return "", errors.New("两次密码不一致")
	}
	return string(first), nil
}
func run() error {
	if len(os.Args) < 2 {
		return errors.New("用法：creative-admin migrate|create-admin|reset-password|disable-admin|intake-create|intake-cancel|intake-resume|backup|restore|report|release-prepare|release-verify|release-rollback；子命令 --help 查看参数")
	}
	command := os.Args[1]
	switch command {
	case "intake-create", "intake-cancel", "intake-resume":
		return intake(command, os.Args[2:])
	case "backup", "restore", "report", "release-prepare", "release-verify", "release-rollback":
		return lifecycle(command, os.Args[2:])
	}
	if command != "migrate" && command != "create-admin" && command != "reset-password" && command != "disable-admin" {
		return errors.New("未知命令")
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	defaultDB := os.Getenv("CREATIVE_DB")
	if defaultDB == "" {
		defaultDB = "data/creative.db"
	}
	path := flags.String("db", defaultDB, "私有数据库路径")
	username := flags.String("username", "", "管理员账号")
	stdin := flags.Bool("password-stdin", false, "从标准输入读密码")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("不支持额外位置参数")
	}
	if command != "migrate" && *username == "" {
		return errors.New("需要 --username")
	}
	db, err := store.Open(*path)
	if err != nil {
		return err
	}
	defer db.Close()
	if command == "migrate" {
		fmt.Println("迁移校验完成")
		return nil
	}
	service, err := auth.New(db)
	if err != nil {
		return err
	}
	if command == "disable-admin" {
		err = service.Disable(*username)
	} else {
		var value string
		value, err = password(*stdin)
		if err != nil {
			return err
		}
		if command == "create-admin" {
			err = service.CreateAdmin(*username, value)
		} else {
			err = service.ResetPassword(*username, value)
		}
	}
	if err != nil {
		return err
	}
	fmt.Println("管理员操作完成")
	return nil
}
func main() {
	if err := run(); err != nil {
		writer := bufio.NewWriter(os.Stderr)
		fmt.Fprintln(writer, err)
		writer.Flush()
		os.Exit(1)
	}
}

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"time"

	"github.com/yyl1212/creative/internal/research"
)

func readReviewFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("审查工件须为不超过 1 MiB 的普通文件")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 1<<20 {
		return nil, errors.New("审查工件超过 1 MiB")
	}
	return raw, nil
}
func reviewCheck(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("review-check", flag.ContinueOnError)
	file := flags.String("file", "", "单候选模型审查 JSON")
	project := flags.String("project", "", "可选：核对待提交立项包 JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *file == "" || flags.NArg() != 0 {
		return errors.New("用法：review-check --file <审查JSON> [--project <立项包JSON>]")
	}
	raw, err := readReviewFile(*file)
	if err != nil {
		return err
	}
	var pkg []byte
	if *project != "" {
		pkg, err = readReviewFile(*project)
		if err != nil {
			return err
		}
	}
	result, err := research.CheckAgentReview(raw, pkg, time.Now())
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(result)
}

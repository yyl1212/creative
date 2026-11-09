package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrProducerInvalid = errors.New("执行端令牌无效或已撤销")
var ErrProducerConflict = errors.New("执行端已变更或不存在，请刷新")
var ErrProducerInput = errors.New("执行端名称无效或已存在")

type Producer struct {
	ID      string
	Name    string
	Version int
	Enabled bool
}

func producerSecret() string {
	var b [32]byte
	rand.Read(b[:])
	return "cr_" + base64.RawURLEncoding.EncodeToString(b[:])
}
func producerDigest(secret string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(secret))) }
func (a *Service) CreateProducer(name, actor, source string) (Producer, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return Producer{}, "", ErrProducerInput
	}
	p := Producer{ID: "producer-" + rand.Text(), Name: name, Version: 1, Enabled: true}
	secret := producerSecret()
	tx, err := a.db.Begin()
	if err != nil {
		return Producer{}, "", err
	}
	defer tx.Rollback()
	var n int
	if err = tx.QueryRow("SELECT count(*) FROM producers WHERE name=?", name).Scan(&n); err != nil {
		return Producer{}, "", err
	}
	if n != 0 {
		return Producer{}, "", ErrProducerInput
	}
	if _, err = tx.Exec("INSERT INTO producers(id,name,token_hash) VALUES (?,?,?)", p.ID, name, producerDigest(secret)); err != nil {
		return Producer{}, "", err
	}
	if _, err = tx.Exec("INSERT INTO audit_events(action,account,source,object_id,details) VALUES ('producer_created',?,?,?,?)", actor, source, p.ID, `{"before":null,"after":"enabled","version":1}`); err != nil {
		return Producer{}, "", err
	}
	if err = tx.Commit(); err != nil {
		return Producer{}, "", err
	}
	return p, secret, nil
}
func (a *Service) RotateProducer(id string, version int, actor, source string) (Producer, string, error) {
	secret := producerSecret()
	p, err := a.changeProducer(id, version, secret, actor, source)
	if err != nil {
		return Producer{}, "", err
	}
	return p, secret, nil
}
func (a *Service) RevokeProducer(id string, version int, actor, source string) error {
	_, err := a.changeProducer(id, version, "", actor, source)
	return err
}
func (a *Service) changeProducer(id string, version int, secret, actor, source string) (Producer, error) {
	tx, err := a.db.Begin()
	if err != nil {
		return Producer{}, err
	}
	defer tx.Rollback()
	var p Producer
	err = tx.QueryRow("SELECT id,name,version,token_hash IS NOT NULL FROM producers WHERE id=? AND version=?", id, version).Scan(&p.ID, &p.Name, &p.Version, &p.Enabled)
	if err == sql.ErrNoRows {
		return Producer{}, ErrProducerConflict
	}
	if err != nil {
		return Producer{}, err
	}
	before := p.Enabled
	var digest any
	action := "producer_revoked"
	if secret != "" {
		digest = producerDigest(secret)
		action = "producer_rotated"
	}
	if _, err = tx.Exec("UPDATE producers SET token_hash=?,version=version+1 WHERE id=? AND version=?", digest, id, version); err != nil {
		return Producer{}, err
	}
	p.Version++
	p.Enabled = secret != ""
	detail := fmt.Sprintf(`{"before_enabled":%t,"after_enabled":%t,"version":%d}`, before, p.Enabled, p.Version)
	if _, err = tx.Exec("INSERT INTO audit_events(action,account,source,object_id,details) VALUES (?,?,?,?,?)", action, actor, source, id, detail); err != nil {
		return Producer{}, err
	}
	return p, tx.Commit()
}
func (a *Service) AuthenticateProducer(secret string) (Producer, error) {
	if len(secret) != 46 || !strings.HasPrefix(secret, "cr_") {
		return Producer{}, ErrProducerInvalid
	}
	var p Producer
	err := a.db.QueryRow("SELECT id,name,version,token_hash IS NOT NULL FROM producers WHERE token_hash=?", producerDigest(secret)).Scan(&p.ID, &p.Name, &p.Version, &p.Enabled)
	if err == sql.ErrNoRows {
		return Producer{}, ErrProducerInvalid
	}
	return p, err
}
func (a *Service) Producers() ([]Producer, error) {
	rows, err := a.db.Query("SELECT id,name,version,token_hash IS NOT NULL FROM producers ORDER BY created_at DESC,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ps := []Producer{}
	for rows.Next() {
		var p Producer
		if err = rows.Scan(&p.ID, &p.Name, &p.Version, &p.Enabled); err != nil {
			return nil, err
		}
		ps = append(ps, p)
	}
	return ps, rows.Err()
}

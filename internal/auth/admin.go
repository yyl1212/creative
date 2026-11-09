package auth

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/alexedwards/argon2id"
)

var ErrInvalid = errors.New("账号或密码不正确")
var ErrThrottled = errors.New("尝试过于频繁，请稍后再试")
var usernamePattern = regexp.MustCompile(`^[a-z0-9_-]{3,64}$`)
var hashParams = &argon2id.Params{Memory: 64 * 1024, Iterations: 3, Parallelism: 2, SaltLength: 16, KeyLength: 32}

type User struct {
	ID       int
	Username string
	Version  int
}
type Service struct {
	db        *sql.DB
	dummyHash string
	slots     chan struct{}
}

func New(db *sql.DB) (*Service, error) {
	dummy, err := argon2id.CreateHash("unavailable-account-password", hashParams)
	if err != nil {
		return nil, err
	}
	return &Service{db: db, dummyHash: dummy, slots: make(chan struct{}, 2)}, nil
}
func normalize(username string) string { return strings.ToLower(strings.TrimSpace(username)) }
func validate(username, password string) error {
	if !usernamePattern.MatchString(username) {
		return errors.New("账号须为 3–64 位小写字母、数字、下划线或连字符")
	}
	if len(password) < 12 || len(password) > 128 {
		return errors.New("密码须为 12–128 字节")
	}
	return nil
}
func (a *Service) CreateAdmin(username, password string) error {
	username = normalize(username)
	if err := validate(username, password); err != nil {
		return err
	}
	hash, err := argon2id.CreateHash(password, hashParams)
	if err != nil {
		return err
	}
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO admins(username,password_hash) VALUES (?,?)", username, hash); err != nil {
		return errors.New("创建失败：账号已存在或数据库不可写")
	}
	if _, err = tx.Exec("INSERT INTO audit_events(action,account,source) VALUES ('account_created',?,'private-cli')", username); err != nil {
		return err
	}
	return tx.Commit()
}
func (a *Service) ResetPassword(username, password string) error {
	username = normalize(username)
	if err := validate(username, password); err != nil {
		return err
	}
	hash, err := argon2id.CreateHash(password, hashParams)
	if err != nil {
		return err
	}
	return a.changeAccount(username, hash, false)
}
func (a *Service) Disable(username string) error {
	return a.changeAccount(normalize(username), "", true)
}
func (a *Service) changeAccount(username, hash string, disable bool) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var result sql.Result
	action := "password_reset"
	if disable {
		action = "account_disabled"
		result, err = tx.Exec("UPDATE admins SET enabled=0,auth_version=auth_version+1 WHERE username=?", username)
	} else {
		result, err = tx.Exec("UPDATE admins SET password_hash=?,enabled=1,auth_version=auth_version+1 WHERE username=?", hash, username)
	}
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("账号不存在")
	}
	if _, err = tx.Exec("DELETE FROM login_limits WHERE key=?", limitKey("account", username)); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO audit_events(action,account,source) VALUES (?,?,'private-cli')", action, username); err != nil {
		return err
	}
	return tx.Commit()
}
func limitKey(kind, value string) string {
	return fmt.Sprintf("%s:%x", kind, sha256.Sum256([]byte(value)))
}
func (a *Service) reserve(username, source string) error {
	now := time.Now().Unix()
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM login_limits WHERE expires<=?", now); err != nil {
		return err
	}
	keys := []string{limitKey("account", username), limitKey("source", source)}
	for _, key := range keys {
		var attempts int
		err = tx.QueryRow("SELECT attempts FROM login_limits WHERE key=?", key).Scan(&attempts)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if attempts >= 5 {
			return ErrThrottled
		}
	}
	for _, key := range keys {
		if _, err = tx.Exec("INSERT INTO login_limits(key,attempts,expires) VALUES (?,1,?) ON CONFLICT(key) DO UPDATE SET attempts=attempts+1", key, now+900); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (a *Service) Record(action, account, source string) error {
	_, err := a.db.Exec("INSERT INTO audit_events(action,account,source) VALUES (?,?,?)", action, account, source)
	return err
}
func (a *Service) Authenticate(username, password, source string) (User, error) {
	username = normalize(username)
	if !usernamePattern.MatchString(username) || len(password) > 128 {
		return User{}, ErrInvalid
	}
	// 固定并发数约束 Argon2 内存占用，避免并发登录耗尽进程。
	select {
	case a.slots <- struct{}{}:
		defer func() { <-a.slots }()
	default:
		return User{}, ErrThrottled
	}
	if err := a.reserve(username, source); err != nil {
		if errors.Is(err, ErrThrottled) {
			if auditErr := a.Record("login_throttled", username, source); auditErr != nil {
				return User{}, auditErr
			}
		}
		return User{}, err
	}
	var user User
	var hash string
	var enabled bool
	err := a.db.QueryRow("SELECT id,username,auth_version,password_hash,enabled FROM admins WHERE username=?", username).Scan(&user.ID, &user.Username, &user.Version, &hash, &enabled)
	if err != nil && err != sql.ErrNoRows {
		return User{}, err
	}
	if err == sql.ErrNoRows {
		hash = a.dummyHash
	}
	matches, err := argon2id.ComparePasswordAndHash(password, hash)
	if err != nil {
		return User{}, err
	}
	if !matches || !enabled {
		if err = a.Record("login_failed", username, source); err != nil {
			return User{}, err
		}
		return User{}, ErrInvalid
	}
	tx, err := a.db.Begin()
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM login_limits WHERE key IN (?,?)", limitKey("account", username), limitKey("source", source)); err != nil {
		return User{}, err
	}
	if _, err = tx.Exec("INSERT INTO audit_events(action,account,source) VALUES ('login_succeeded',?,?)", username, source); err != nil {
		return User{}, err
	}
	return user, tx.Commit()
}
func (a *Service) Current(id, version int) (User, error) {
	var user User
	err := a.db.QueryRow("SELECT id,username,auth_version FROM admins WHERE id=? AND auth_version=? AND enabled=1", id, version).Scan(&user.ID, &user.Username, &user.Version)
	if err == sql.ErrNoRows {
		return User{}, ErrInvalid
	}
	return user, err
}

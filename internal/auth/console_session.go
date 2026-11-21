package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	consoleSessionKeyBytes = 32
	consoleSessionVersion  = "v1"
	consoleSessionKeyName  = "session.key"
)

// ConsoleSession 签发并校验 Web 控制台的登录会话。
//
// 刻意不维护服务端会话表：cookie 自己携带签发时间和到期时间，用 HMAC-SHA256 签名。
// 好处是 Core 重启不会让所有人掉线、也不需要清理过期记录；代价是“吊销单个会话”
// 做不到，只能换签名密钥让全部会话失效。签名密钥因此必须独立随机生成，不能从
// 登录口令派生——从口令派生的 HMAC 一旦配上已知明文（cookie 的载荷是明文），
// 截获的 cookie 就成了离线撞库的样本。
type ConsoleSession struct {
	key []byte
	ttl time.Duration
}

func NewConsoleSession(key []byte, ttl time.Duration) *ConsoleSession {
	return &ConsoleSession{key: key, ttl: ttl}
}

// Enabled 允许调用方持有 nil 指针：未启用远程登录时构造出来就是 nil。
func (s *ConsoleSession) Enabled() bool {
	return s != nil && len(s.key) >= consoleSessionKeyBytes && s.ttl > 0
}

func (s *ConsoleSession) TTL() time.Duration {
	if s == nil {
		return 0
	}
	return s.ttl
}

// Issue 生成 `<版本>.<签发时间>.<到期时间>.<签名>`。签名覆盖前三段，
// 任何一段被改动都会导致校验失败。
func (s *ConsoleSession) Issue(now time.Time) (string, error) {
	if !s.Enabled() {
		return "", errors.New("console session is not configured")
	}
	issued := now.UTC().Truncate(time.Second)
	expires := issued.Add(s.ttl)
	payload := fmt.Sprintf("%s.%d.%d", consoleSessionVersion, issued.Unix(), expires.Unix())
	return payload + "." + s.sign(payload), nil
}

// Valid 只接受本进程签发的、未过期的会话。到期时间必须落在配置的 TTL 窗口内，
// 否则一个用旧（更长）TTL 签发的 cookie 可以在缩短 TTL 后继续存活。
func (s *ConsoleSession) Valid(raw string, now time.Time) bool {
	if !s.Enabled() {
		return false
	}
	fields := strings.Split(strings.TrimSpace(raw), ".")
	if len(fields) != 4 || fields[0] != consoleSessionVersion {
		return false
	}
	issued, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return false
	}
	expires, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return false
	}
	if expires <= now.UTC().Unix() || issued > expires {
		return false
	}
	if expires-issued > int64(s.ttl/time.Second) {
		return false
	}
	payload := fields[0] + "." + fields[1] + "." + fields[2]
	return hmac.Equal([]byte(s.sign(payload)), []byte(fields[3]))
}

func (s *ConsoleSession) sign(payload string) string {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// LoadOrCreateConsoleSessionKey 读取 `<dir>/session.key`，缺失时随机生成并落盘。
// 删除该文件即可让全部已签发的控制台会话立刻失效。
func LoadOrCreateConsoleSessionKey(dir string) ([]byte, error) {
	path := filepath.Join(dir, consoleSessionKeyName)
	key, err := readConsoleSessionKey(path)
	if err == nil {
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create console session key dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("secure console session key dir: %w", err)
	}

	key = make([]byte, consoleSessionKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate console session key: %w", err)
	}
	// 与 public-artifacts 一致：先写同目录临时文件，再用硬链接以“不覆盖”语义发布。
	// 并发启动的两个进程只有一个能创建最终路径，另一个读取胜出的同一份密钥。
	tmp, err := os.CreateTemp(dir, ".console-session-key-*")
	if err != nil {
		return nil, fmt.Errorf("create console session key temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("secure console session key temp file: %w", err)
	}
	if _, err := tmp.WriteString(hex.EncodeToString(key) + "\n"); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("write console session key temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("sync console session key temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("close console session key temp file: %w", err)
	}
	if err := os.Link(tmpPath, path); err == nil {
		return key, nil
	} else if !os.IsExist(err) {
		return nil, fmt.Errorf("publish console session key: %w", err)
	}
	return readConsoleSessionKey(path)
}

func readConsoleSessionKey(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(decoded) < consoleSessionKeyBytes {
		return nil, errors.New("console session key is invalid")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("secure console session key: %w", err)
	}
	return decoded, nil
}

package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testConsoleKey(fill byte) []byte {
	key := make([]byte, consoleSessionKeyBytes)
	for i := range key {
		key[i] = fill
	}
	return key
}

func TestConsoleSessionIssueAndValidate(t *testing.T) {
	session := NewConsoleSession(testConsoleKey(7), time.Hour)
	now := time.Unix(1_800_000_000, 0)

	token, err := session.Issue(now)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if !strings.HasPrefix(token, consoleSessionVersion+".") {
		t.Fatalf("token = %q, want %s prefix", token, consoleSessionVersion)
	}
	if !session.Valid(token, now) {
		t.Fatal("freshly issued token rejected")
	}
	if !session.Valid(token, now.Add(time.Hour-time.Second)) {
		t.Fatal("token rejected before expiry")
	}
	if session.Valid(token, now.Add(time.Hour)) {
		t.Fatal("token accepted at expiry")
	}
}

func TestConsoleSessionRejectsTamperedTokens(t *testing.T) {
	session := NewConsoleSession(testConsoleKey(7), time.Hour)
	now := time.Unix(1_800_000_000, 0)
	token, err := session.Issue(now)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Split(token, ".")
	if len(fields) != 4 {
		t.Fatalf("token = %q, want 4 fields", token)
	}

	cases := map[string]string{
		"flipped signature":  fields[0] + "." + fields[1] + "." + fields[2] + "." + strings.Repeat("A", len(fields[3])),
		"shortened":          token[:len(token)-2],
		"payload only":       fields[0] + "." + fields[1] + "." + fields[2],
		"empty":              "",
		"wrong version":      "v2." + fields[1] + "." + fields[2] + "." + fields[3],
		"garbage issued":     fields[0] + ".zz." + fields[2] + "." + fields[3],
		"issued after exprs": fields[0] + "." + fields[2] + "." + fields[1] + "." + fields[3],
	}
	for name, candidate := range cases {
		if session.Valid(candidate, now) {
			t.Fatalf("%s: tampered token accepted", name)
		}
	}
	// 不同密钥签发的同一载荷必须失效。
	if NewConsoleSession(testConsoleKey(9), time.Hour).Valid(token, now) {
		t.Fatal("token signed by another key accepted")
	}
}

func TestConsoleSessionRejectsExpiryBeyondConfiguredTTL(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	longLived := NewConsoleSession(testConsoleKey(3), 30*24*time.Hour)
	token, err := longLived.Issue(now)
	if err != nil {
		t.Fatal(err)
	}
	// 把 TTL 缩短后，旧的长寿命 Cookie 不能继续用：到期时间必须落在新窗口内。
	shortLived := NewConsoleSession(testConsoleKey(3), time.Hour)
	if shortLived.Valid(token, now) {
		t.Fatal("token issued with a longer TTL outlived the shortened TTL")
	}
}

func TestConsoleSessionDisabledWithoutKeyOrTTL(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for name, session := range map[string]*ConsoleSession{
		"nil":       nil,
		"short key": NewConsoleSession([]byte("too-short"), time.Hour),
		"zero ttl":  NewConsoleSession(testConsoleKey(1), 0),
		"empty key": NewConsoleSession(nil, time.Hour),
	} {
		if session.Enabled() {
			t.Fatalf("%s: Enabled() = true", name)
		}
		if session.Valid("v1.1.2.abc", now) {
			t.Fatalf("%s: Valid() = true", name)
		}
		if _, err := session.Issue(now); err == nil {
			t.Fatalf("%s: Issue() succeeded", name)
		}
	}
}

func TestLoadOrCreateConsoleSessionKeyIsStableAndPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "remote-auth")
	first, err := LoadOrCreateConsoleSessionKey(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateConsoleSessionKey() error = %v", err)
	}
	if len(first) != consoleSessionKeyBytes {
		t.Fatalf("key length = %d, want %d", len(first), consoleSessionKeyBytes)
	}
	second, err := LoadOrCreateConsoleSessionKey(dir)
	if err != nil {
		t.Fatalf("reload error = %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("key changed between loads; sessions would not survive restart")
	}
	info, err := os.Stat(filepath.Join(dir, consoleSessionKeyName))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("key mode = %o, want 600", mode)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Fatalf("key dir mode = %o, want 700", mode)
	}
}

func TestLoadOrCreateConsoleSessionKeyRejectsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, consoleSessionKeyName), []byte("not-hex"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateConsoleSessionKey(dir); err == nil {
		t.Fatal("corrupt key file accepted")
	}
}

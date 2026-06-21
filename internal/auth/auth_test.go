package auth

import (
	"testing"
	"time"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("哈希失败: %v", err)
	}
	if hash == "correct horse battery staple" {
		t.Fatal("哈希串不应等于明文")
	}

	ok, err := VerifyPassword("correct horse battery staple", hash)
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if !ok {
		t.Fatal("正确密码应校验通过")
	}

	ok, err = VerifyPassword("wrong password", hash)
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if ok {
		t.Fatal("错误密码不应校验通过")
	}
}

func TestVerifyPasswordInvalidHash(t *testing.T) {
	if _, err := VerifyPassword("x", "not-a-valid-hash"); err == nil {
		t.Fatal("非法哈希应返回错误")
	}
}

func TestTokenIssueAndParse(t *testing.T) {
	m := NewTokenManager("test-secret", time.Hour)
	now := time.Now()

	tok, err := m.Issue("user-1", "admin", now)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}

	claims, err := m.Parse(tok)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if claims.UserID != "user-1" || claims.Role != "admin" {
		t.Fatalf("载荷不符: %+v", claims)
	}
}

func TestTokenExpired(t *testing.T) {
	m := NewTokenManager("test-secret", time.Hour)
	// 签发一个一小时前就过期的令牌
	tok, err := m.Issue("user-1", "user", time.Now().Add(-2*time.Hour))
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	if _, err := m.Parse(tok); err == nil {
		t.Fatal("过期令牌应解析失败")
	}
}

func TestTokenWrongSecret(t *testing.T) {
	a := NewTokenManager("secret-a", time.Hour)
	b := NewTokenManager("secret-b", time.Hour)
	tok, _ := a.Issue("user-1", "user", time.Now())
	if _, err := b.Parse(tok); err == nil {
		t.Fatal("不同密钥应解析失败")
	}
}

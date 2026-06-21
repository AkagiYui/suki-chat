package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrInvalidToken 表示令牌无效或已过期。
var ErrInvalidToken = errors.New("auth: 令牌无效或已过期")

// Claims 是 JWT 的载荷。
type Claims struct {
	UserID string `json:"uid"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

// TokenManager 负责签发与校验 JWT。
type TokenManager struct {
	secret []byte
	ttl    time.Duration
}

// NewTokenManager 创建令牌管理器。
func NewTokenManager(secret string, ttl time.Duration) *TokenManager {
	return &TokenManager{secret: []byte(secret), ttl: ttl}
}

// Issue 为指定用户签发 access token。now 由调用方传入以便测试。
func (m *TokenManager) Issue(userID, role string, now time.Time) (string, error) {
	claims := Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
}

// Parse 校验令牌并返回载荷。
func (m *TokenManager) Parse(token string) (*Claims, error) {
	claims := new(Claims)
	parsed, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return m.secret, nil
	})
	if err != nil || !parsed.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

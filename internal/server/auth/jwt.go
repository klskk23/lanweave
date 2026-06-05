package auth

import (
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the identity carried by a verified session token.
type Claims struct {
	UserID   int64
	Username string
	IsAdmin  bool
}

// JWTManager issues and verifies HS256 session tokens.
type JWTManager struct {
	secret []byte
	ttl    time.Duration
}

// NewJWTManager builds a manager from the configured secret and token lifetime.
func NewJWTManager(secret string, ttl time.Duration) *JWTManager {
	return &JWTManager{secret: []byte(secret), ttl: ttl}
}

type jwtClaims struct {
	Username string `json:"username"`
	IsAdmin  bool   `json:"is_admin"`
	jwt.RegisteredClaims
}

// Issue returns a signed token for the given identity, expiring after the ttl.
func (m *JWTManager) Issue(c Claims) (string, error) {
	now := time.Now()
	claims := jwtClaims{
		Username: c.Username,
		IsAdmin:  c.IsAdmin,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(c.UserID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
}

// Verify parses and validates a token, returning its identity. It pins HS256 so
// a token claiming a different algorithm is rejected before the key is used, and
// rejects expired or tampered tokens.
func (m *JWTManager) Verify(tokenStr string) (*Claims, error) {
	var jc jwtClaims
	_, err := jwt.ParseWithClaims(tokenStr, &jc, func(*jwt.Token) (any, error) {
		return m.secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, err
	}
	uid, err := strconv.ParseInt(jc.Subject, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid token subject: %w", err)
	}
	return &Claims{UserID: uid, Username: jc.Username, IsAdmin: jc.IsAdmin}, nil
}

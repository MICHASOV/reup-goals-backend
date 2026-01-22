package auth

import (
	"github.com/golang-jwt/jwt/v5"
	"time"
)

func GenerateToken(secret []byte, userID int) (string, error) {
	claims := jwt.MapClaims{
		"user_id": userID,
		"exp":     time.Now().Add(30 * 24 * time.Hour).Unix(),
		"iat":     time.Now().Unix(),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(secret)
}

func ParseToken(secret []byte, tokenString string) (int, error) {
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		return secret, nil
	})
	if err != nil || !token.Valid {
		return 0, err
	}

	data := token.Claims.(jwt.MapClaims)
	uidFloat, ok := data["user_id"].(float64)
	if !ok {
		return 0, err
	}
	return int(uidFloat), nil
}

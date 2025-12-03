package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"reup-goals-backend/internal/db"
)

var jwtSecret = []byte("SUPER_SECRET_KEY_CHANGE_ME") // ⚠️ замени на env

type AuthHandler struct {
	DB *db.DB
}

type User struct {
	ID       int64  `db:"id"`
	Email    string `db:"email"`
	Password string `db:"password"`
}

type AuthRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type AuthResponse struct {
	Token string `json:"token"`
}

func NewAuthHandler(db *db.DB) *AuthHandler {
	return &AuthHandler{DB: db}
}

// ------------------------------------------------------------------
// Registration: POST /auth/register
// ------------------------------------------------------------------

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req AuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", 400)
		return
	}

	// check duplicate email
	var exists int
	err := h.DB.QueryRow(
		context.Background(),
		"SELECT COUNT(*) FROM users WHERE email=$1", req.Email,
	).Scan(&exists)

	if err == nil && exists > 0 {
		http.Error(w, "email already exists", 400)
		return
	}

	// hash password
	hash, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)

	// insert user
	var id int64
	err = h.DB.QueryRow(
		context.Background(),
		"INSERT INTO users (email, password) VALUES ($1, $2) RETURNING id",
		req.Email, string(hash),
	).Scan(&id)

	if err != nil {
		http.Error(w, "db error: "+err.Error(), 500)
		return
	}

	token, _ := generateToken(id)

	json.NewEncoder(w).Encode(AuthResponse{Token: token})
}

// ------------------------------------------------------------------
// Login: POST /auth/login
// ------------------------------------------------------------------

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req AuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", 400)
		return
	}

	var user User
	err := h.DB.QueryRow(
		context.Background(),
		"SELECT id, email, password FROM users WHERE email=$1",
		req.Email,
	).Scan(&user.ID, &user.Email, &user.Password)

	if err != nil {
		http.Error(w, "invalid credentials", 403)
		return
	}

	// compare password
	if bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)) != nil {
		http.Error(w, "invalid credentials", 403)
		return
	}

	token, _ := generateToken(user.ID)

	json.NewEncoder(w).Encode(AuthResponse{Token: token})
}

// ------------------------------------------------------------------
// Get current user: GET /auth/me
// ------------------------------------------------------------------

func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("user_id")

	if userID == nil {
		http.Error(w, "unauthorized", 401)
		return
	}

	var email string
	err := h.DB.QueryRow(
		context.Background(),
		"SELECT email FROM users WHERE id=$1", userID,
	).Scan(&email)

	if err != nil {
		http.Error(w, "user not found", 404)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":    userID,
		"email": email,
	})
}

// ------------------------------------------------------------------
// Middleware
// ------------------------------------------------------------------

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// read bearer
		bearer := r.Header.Get("Authorization")
		if bearer == "" || len(bearer) < 8 {
			http.Error(w, "missing token", 401)
			return
		}

		tokenStr := bearer[len("Bearer "):]

		token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
			return jwtSecret, nil
		})

		if err != nil || !token.Valid {
			http.Error(w, "invalid token", 401)
			return
		}

		claims := token.Claims.(jwt.MapClaims)
		id := int64(claims["id"].(float64))

		ctx := context.WithValue(r.Context(), "user_id", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ------------------------------------------------------------------
// Token generator
// ------------------------------------------------------------------

func generateToken(id int64) (string, error) {
	claims := jwt.MapClaims{
		"id":  id,
		"exp": time.Now().Add(7 * 24 * time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}
package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"reup-goals-backend/internal/db"
)

var jwtSecret = []byte("SUPER_SECRET_KEY_CHANGE_ME") // ⚠️ замени на env

type AuthHandler struct {
	DB *db.DB
}

type User struct {
	ID       int64  `db:"id"`
	Email    string `db:"email"`
	Password string `db:"password"`
}

type AuthRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type AuthResponse struct {
	Token string `json:"token"`
}

func NewAuthHandler(db *db.DB) *AuthHandler {
	return &AuthHandler{DB: db}
}

// ------------------------------------------------------------------
// Registration: POST /auth/register
// ------------------------------------------------------------------

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req AuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", 400)
		return
	}

	// check duplicate email
	var exists int
	err := h.DB.QueryRow(
		context.Background(),
		"SELECT COUNT(*) FROM users WHERE email=$1", req.Email,
	).Scan(&exists)

	if err == nil && exists > 0 {
		http.Error(w, "email already exists", 400)
		return
	}

	// hash password
	hash, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)

	// insert user
	var id int64
	err = h.DB.QueryRow(
		context.Background(),
		"INSERT INTO users (email, password) VALUES ($1, $2) RETURNING id",
		req.Email, string(hash),
	).Scan(&id)

	if err != nil {
		http.Error(w, "db error: "+err.Error(), 500)
		return
	}

	token, _ := generateToken(id)

	json.NewEncoder(w).Encode(AuthResponse{Token: token})
}

// ------------------------------------------------------------------
// Login: POST /auth/login
// ------------------------------------------------------------------

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req AuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", 400)
		return
	}

	var user User
	err := h.DB.QueryRow(
		context.Background(),
		"SELECT id, email, password FROM users WHERE email=$1",
		req.Email,
	).Scan(&user.ID, &user.Email, &user.Password)

	if err != nil {
		http.Error(w, "invalid credentials", 403)
		return
	}

	// compare password
	if bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)) != nil {
		http.Error(w, "invalid credentials", 403)
		return
	}

	token, _ := generateToken(user.ID)

	json.NewEncoder(w).Encode(AuthResponse{Token: token})
}

// ------------------------------------------------------------------
// Get current user: GET /auth/me
// ------------------------------------------------------------------

func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("user_id")

	if userID == nil {
		http.Error(w, "unauthorized", 401)
		return
	}

	var email string
	err := h.DB.QueryRow(
		context.Background(),
		"SELECT email FROM users WHERE id=$1", userID,
	).Scan(&email)

	if err != nil {
		http.Error(w, "user not found", 404)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":    userID,
		"email": email,
	})
}

// ------------------------------------------------------------------
// Middleware
// ------------------------------------------------------------------

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// read bearer
		bearer := r.Header.Get("Authorization")
		if bearer == "" || len(bearer) < 8 {
			http.Error(w, "missing token", 401)
			return
		}

		tokenStr := bearer[len("Bearer "):]

		token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
			return jwtSecret, nil
		})

		if err != nil || !token.Valid {
			http.Error(w, "invalid token", 401)
			return
		}

		claims := token.Claims.(jwt.MapClaims)
		id := int64(claims["id"].(float64))

		ctx := context.WithValue(r.Context(), "user_id", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ------------------------------------------------------------------
// Token generator
// ------------------------------------------------------------------

func generateToken(id int64) (string, error) {
	claims := jwt.MapClaims{
		"id":  id,
		"exp": time.Now().Add(7 * 24 * time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}

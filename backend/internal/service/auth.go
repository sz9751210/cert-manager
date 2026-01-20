package service

import (
	"cert-manager/internal/domain"
	"context"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/crypto/bcrypt"
)

type AuthService struct {
	Collection *mongo.Collection
	JWTSecret  []byte
}

func NewAuthService(db *mongo.Database, secret string) *AuthService {
	return &AuthService{
		Collection: db.Collection("users"),
		JWTSecret:  []byte(secret),
	}
}

// 初始化預設管理者 (如果沒有用戶的話)
func (s *AuthService) InitAdmin() {
	count, _ := s.Collection.CountDocuments(context.Background(), bson.M{})
	if count == 0 {
		hashed, _ := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
		admin := domain.User{Username: "admin", Password: string(hashed)}
		s.Collection.InsertOne(context.Background(), admin)
	}
}

// Login 驗證並回傳 Token
func (s *AuthService) Login(ctx context.Context, username, password string) (string, error) {
	var user domain.User
	err := s.Collection.FindOne(ctx, bson.M{"username": username}).Decode(&user)
	if err != nil {
		return "", errors.New("用戶不存在或密碼錯誤")
	}

	// 比對密碼
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		return "", errors.New("用戶不存在或密碼錯誤")
	}

	// 生成 JWT
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": user.ID.Hex(),
		"exp": time.Now().Add(24 * time.Hour).Unix(), // 24小時過期
	})

	return token.SignedString(s.JWTSecret)
}

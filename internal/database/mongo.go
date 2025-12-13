package database

import (
	"context"
	"time"

	"cert-manager/internal/conf"

	"github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Connect 初始化 MongoDB 連線
func Connect(cfg conf.MongoConfig) (*mongo.Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	clientOptions := options.Client().ApplyURI(cfg.URI)
	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		return nil, err
	}

	// 測試連線 (Ping)
	if err := client.Ping(ctx, nil); err != nil {
		return nil, err
	}

	logrus.Info("成功連線至 MongoDB")
	return client, nil
}

package repository

import (
	"cert-manager/internal/domain"
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type DomainRepository interface {
	Upsert(ctx context.Context, cert domain.SSLCertificate) error
	List(ctx context.Context, page int64, pageSize int64, sortBy string) ([]domain.SSLCertificate, int64, error)
	UpdateCertInfo(ctx context.Context, cert domain.SSLCertificate) error
}

type mongoDomainRepo struct {
	collection *mongo.Collection
}

func NewMongoDomainRepo(db *mongo.Database) DomainRepository {
	return &mongoDomainRepo{
		collection: db.Collection("domains"),
	}
}

// Upsert: 根據 DomainName 和 CFRecordID 判斷，有則更新，無則新增
func (r *mongoDomainRepo) Upsert(ctx context.Context, cert domain.SSLCertificate) error {
	filter := bson.M{
		"domain_name":  cert.DomainName,
		"cf_record_id": cert.CFRecordID,
	}

	update := bson.M{
		"$set": bson.M{
			"cf_zone_id": cert.CFZoneID,
			"is_proxied": cert.IsProxied,
			"status":     cert.Status,
			// 注意：我們不更新 "is_ignored" 和 "auto_renew"，以免覆蓋使用者設定
		},
		"$setOnInsert": bson.M{
			"created_at": time.Now(),
			"is_ignored": false, // 預設值
		},
	}

	opts := options.Update().SetUpsert(true)
	_, err := r.collection.UpdateOne(ctx, filter, update, opts)
	return err
}

// List: 支援分頁與排序
func (r *mongoDomainRepo) List(ctx context.Context, page int64, pageSize int64, sortBy string) ([]domain.SSLCertificate, int64, error) {
	// 1. 計算 Skip
	skip := (page - 1) * pageSize

	// 2. 設定排序 (Sort)
	sortOpts := bson.D{}
	if sortBy == "expiry_asc" {
		sortOpts = bson.D{{Key: "not_after", Value: 1}} // 過期日由近到遠
	} else if sortBy == "expiry_desc" {
		sortOpts = bson.D{{Key: "not_after", Value: -1}}
	} else {
		sortOpts = bson.D{{Key: "_id", Value: -1}} // 預設新加入的在前面
	}

	findOptions := options.Find()
	findOptions.SetSkip(skip)
	findOptions.SetLimit(pageSize)
	findOptions.SetSort(sortOpts)

	// 3. 執行查詢
	cursor, err := r.collection.Find(ctx, bson.M{}, findOptions)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	var results []domain.SSLCertificate
	if err = cursor.All(ctx, &results); err != nil {
		return nil, 0, err
	}

	// 4. 計算總數 (給前端分頁元件用)
	total, err := r.collection.CountDocuments(ctx, bson.M{})
	if err != nil {
		return nil, 0, err
	}

	return results, total, nil
}

// 2. 在檔案最下方新增這個方法的實作
func (r *mongoDomainRepo) UpdateCertInfo(ctx context.Context, cert domain.SSLCertificate) error {
	filter := bson.M{"_id": cert.ID}

	update := bson.M{
		"$set": bson.M{
			"issuer":          cert.Issuer,
			"not_before":      cert.NotBefore,
			"not_after":       cert.NotAfter,
			"days_remaining":  cert.DaysRemaining,
			"status":          cert.Status,
			"error_msg":       cert.ErrorMsg,
			"last_check_time": time.Now(),
		},
	}

	_, err := r.collection.UpdateOne(ctx, filter, update)
	return err
}

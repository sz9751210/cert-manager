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
	List(ctx context.Context, page int64, pageSize int64, sortBy string, statusFilter string) ([]domain.SSLCertificate, int64, error)
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
// 2. 修改 List 實作
func (r *mongoDomainRepo) List(ctx context.Context, page int64, pageSize int64, sortBy string, statusFilter string) ([]domain.SSLCertificate, int64, error) {
	skip := (page - 1) * pageSize

	// 建構過濾條件
	filter := bson.M{}

	// 處理狀態過濾
	if statusFilter == "unresolvable" {
		// 只看無法解析的
		filter["status"] = "unresolvable"
	} else if statusFilter == "active_only" {
		// 排除無法解析的 (顯示正常、過期、警告)
		filter["status"] = bson.M{"$ne": "unresolvable"}
	}
	// 如果 statusFilter 為空，就顯示全部

	sortOpts := bson.D{}
	if sortBy == "expiry_asc" {
		sortOpts = bson.D{{Key: "not_after", Value: 1}}
	} else if sortBy == "expiry_desc" {
		sortOpts = bson.D{{Key: "not_after", Value: -1}}
	} else {
		sortOpts = bson.D{{Key: "_id", Value: -1}}
	}

	findOptions := options.Find()
	findOptions.SetSkip(skip)
	findOptions.SetLimit(pageSize)
	findOptions.SetSort(sortOpts)

	cursor, err := r.collection.Find(ctx, filter, findOptions)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	var results []domain.SSLCertificate
	if err = cursor.All(ctx, &results); err != nil {
		return nil, 0, err
	}

	// 計算符合過濾條件的總數
	total, err := r.collection.CountDocuments(ctx, filter)
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

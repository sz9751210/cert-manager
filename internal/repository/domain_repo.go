package repository

import (
	"cert-manager/internal/domain"
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// 1. 修改介面簽章 (加入 proxiedFilter 和 ignoredFilter)
// proxiedFilter: "true" (只顯Proxy), "false" (只顯非Proxy), "" (全部)
// ignoredFilter: "true" (顯示忽略的), "false" (隱藏忽略的-預設)
type DomainRepository interface {
	Upsert(ctx context.Context, cert domain.SSLCertificate) error
	List(ctx context.Context, page, pageSize int64, sortBy, statusFilter, proxiedFilter, ignoredFilter, zoneFilter string) ([]domain.SSLCertificate, int64, error)
	UpdateCertInfo(ctx context.Context, cert domain.SSLCertificate) error
	// [新增] 更新設定 (用於切換是否忽略)
	UpdateSettings(ctx context.Context, id string, isIgnored bool) error
	GetUniqueZones(ctx context.Context) ([]string, error)

	// [新增] 設定相關
	GetSettings(ctx context.Context) (*domain.NotificationSettings, error)
	SaveSettings(ctx context.Context, settings domain.NotificationSettings) error

	// [新增] 更新告警時間
	UpdateAlertTime(ctx context.Context, domainID primitive.ObjectID) error

	GetStatistics(ctx context.Context) (*domain.DashboardStats, error)
}

type mongoDomainRepo struct {
	collection *mongo.Collection
}

// 實作 GetStatistics
func (r *mongoDomainRepo) GetStatistics(ctx context.Context) (*domain.DashboardStats, error) {
	stats := &domain.DashboardStats{
		StatusCounts: make(map[string]int),
		ExpiryCounts: make(map[string]int),
		IssuerCounts: make(map[string]int),
	}

	// 1. 總數 (只算未忽略的)
	total, _ := r.collection.CountDocuments(ctx, bson.M{"is_ignored": false})
	stats.TotalDomains = total

	// 2. 撈取所有未忽略的資料進行統計 (如果資料量 < 10萬，直接用 Find 遍歷記憶體統計通常比 Aggregation Pipeline 寫起來簡單且夠快)
	// 為了教學簡單，我們這裡採用「查出所有簡要欄位」在 Go 裡面算，這比寫 MongoDB 複雜 pipeline 容易除錯
	cursor, err := r.collection.Find(ctx, bson.M{"is_ignored": false}, options.Find().SetProjection(bson.M{
		"status": 1, "days_remaining": 1, "issuer": 1,
	}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	type miniCert struct {
		Status        string `bson:"status"`
		DaysRemaining int    `bson:"days_remaining"`
		Issuer        string `bson:"issuer"`
	}

	for cursor.Next(ctx) {
		var c miniCert
		if err := cursor.Decode(&c); err != nil {
			continue
		}

		// 統計狀態
		stats.StatusCounts[c.Status]++

		// 統計發行商 (簡單清理字串)
		if c.Issuer != "" {
			stats.IssuerCounts[c.Issuer]++
		} else {
			stats.IssuerCounts["Unknown"]++
		}

		// 統計過期區間
		// 注意：只有 active/warning 的才需要算剩餘天數
		if c.Status != "unresolvable" && c.Status != "pending" {
			if c.DaysRemaining < 7 {
				stats.ExpiryCounts["d7"]++
			}
			if c.DaysRemaining < 30 {
				stats.ExpiryCounts["d30"]++
			}
			if c.DaysRemaining < 90 {
				stats.ExpiryCounts["d90"]++
			}
		}
	}

	return stats, nil
}

func NewMongoDomainRepo(db *mongo.Database) DomainRepository {
	return &mongoDomainRepo{
		collection: db.Collection("domains"),
	}
}

// 1. 實作 GetUniqueZones (使用 MongoDB Distinct)
func (r *mongoDomainRepo) GetUniqueZones(ctx context.Context) ([]string, error) {
	// 撈出 distinct "zone_name"
	values, err := r.collection.Distinct(ctx, "zone_name", bson.M{})
	if err != nil {
		return nil, err
	}

	var zones []string
	for _, v := range values {
		if str, ok := v.(string); ok {
			zones = append(zones, str)
		}
	}
	return zones, nil
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
			"zone_name":  cert.ZoneName,
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
func (r *mongoDomainRepo) List(ctx context.Context, page, pageSize int64, sortBy, statusFilter, proxiedFilter, ignoredFilter, zoneFilter string) ([]domain.SSLCertificate, int64, error) {
	skip := (page - 1) * pageSize

	// 建構過濾條件
	filter := bson.M{}

	// [新增] 主域名過濾
	if zoneFilter != "" {
		filter["zone_name"] = zoneFilter
	}
	// 處理狀態過濾
	if statusFilter == "unresolvable" {
		// 只看無法解析的
		filter["status"] = "unresolvable"
	} else if statusFilter == "active_only" {
		// 排除無法解析的 (顯示正常、過期、警告)
		filter["status"] = bson.M{"$ne": "unresolvable"}
	}
	// 如果 statusFilter 為空，就顯示全部

	// [新增] Proxy 過濾
	if proxiedFilter == "true" {
		filter["is_proxied"] = true
	} else if proxiedFilter == "false" {
		filter["is_proxied"] = false
	}

	// [修改這裡] 更精確的忽略狀態過濾
	if ignoredFilter == "true" {
		// 模式 A: 只顯示「已忽略」的域名 (給新頁面用)
		filter["is_ignored"] = true
	} else if ignoredFilter == "false" || ignoredFilter == "" {
		// 模式 B: 只顯示「監控中」的域名 (給儀表板用 - 預設)
		filter["is_ignored"] = false
	}
	// 如果 ignoredFilter == "true"，我們就不加這個條件，代表全部顯示 (包含忽略的)
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

// 3. [新增] 實作 UpdateSettings
func (r *mongoDomainRepo) UpdateSettings(ctx context.Context, id string, isIgnored bool) error {
	oid, _ := primitive.ObjectIDFromHex(id)
	filter := bson.M{"_id": oid}
	update := bson.M{
		"$set": bson.M{"is_ignored": isIgnored},
	}
	_, err := r.collection.UpdateOne(ctx, filter, update)
	return err
}

// [實作] GetSettings
func (r *mongoDomainRepo) GetSettings(ctx context.Context) (*domain.NotificationSettings, error) {
	// 我們將設定存放在一個獨立的 collection 叫 "settings"
	// 因為只有一筆全域設定，我們固定 ID 或只取第一筆
	coll := r.collection.Database().Collection("settings")

	var settings domain.NotificationSettings
	// 嘗試抓取第一筆
	err := coll.FindOne(ctx, bson.M{}).Decode(&settings)
	if err == mongo.ErrNoDocuments {
		return &domain.NotificationSettings{}, nil // 回傳空設定
	}
	return &settings, err
}

// [實作] SaveSettings
func (r *mongoDomainRepo) SaveSettings(ctx context.Context, settings domain.NotificationSettings) error {
	coll := r.collection.Database().Collection("settings")
	// 使用 Upsert，確保只有一筆設定
	opts := options.Update().SetUpsert(true)
	_, err := coll.UpdateOne(ctx, bson.M{}, bson.M{"$set": settings}, opts)
	return err
}

// [實作] UpdateAlertTime
func (r *mongoDomainRepo) UpdateAlertTime(ctx context.Context, domainID primitive.ObjectID) error {
	filter := bson.M{"_id": domainID}
	update := bson.M{"$set": bson.M{"last_alert_time": time.Now()}}
	_, err := r.collection.UpdateOne(ctx, filter, update)
	return err
}

package repository

import (
	"cert-manager/internal/domain"
	"context"
	"time"

	"github.com/sirupsen/logrus"
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
	List(ctx context.Context, page, pageSize int64, sortBy, search, statusFilter, proxiedFilter, ignoredFilter, zoneFilter string) ([]domain.SSLCertificate, int64, error)
	UpdateCertInfo(ctx context.Context, cert domain.SSLCertificate) error
	// [新增] 更新設定 (用於切換是否忽略)
	UpdateSettings(ctx context.Context, id string, isIgnored bool, port int) error
	GetUniqueZones(ctx context.Context) ([]string, error)

	// [新增] 設定相關
	GetSettings(ctx context.Context) (*domain.NotificationSettings, error)
	SaveSettings(ctx context.Context, settings domain.NotificationSettings) error

	// [新增] 更新告警時間
	UpdateAlertTime(ctx context.Context, domainID primitive.ObjectID) error

	GetStatistics(ctx context.Context) (*domain.DashboardStats, error)

	UpdateAcmeData(ctx context.Context, email, privateKey, regData string) error

	BatchUpdateSettings(ctx context.Context, ids []primitive.ObjectID, isIgnored bool) error // [新增]

	Create(ctx context.Context, cert domain.SSLCertificate) error
	GetByID(ctx context.Context, id primitive.ObjectID) (*domain.SSLCertificate, error)
	Delete(ctx context.Context, id primitive.ObjectID) error
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

	// [新增] B. 暫停監控總數 (已忽略)
	ignoredCount, _ := r.collection.CountDocuments(ctx, bson.M{"is_ignored": true})
	stats.IgnoredDomains = int(ignoredCount) // 需在 Model 新增此欄位

	// [新增] C. 主域名總數 (Unique Zone Name)
	// 這裡統計所有域名(包含忽略的)的主域名數量，或者您可以只統計未忽略的
	zones, _ := r.collection.Distinct(ctx, "zone_name", bson.M{})
	stats.TotalZones = len(zones) // 需在 Model 新增此欄位

	// 2. 撈取所有未忽略的資料進行統計 (如果資料量 < 10萬，直接用 Find 遍歷記憶體統計通常比 Aggregation Pipeline 寫起來簡單且夠快)
	// 為了教學簡單，我們這裡採用「查出所有簡要欄位」在 Go 裡面算，這比寫 MongoDB 複雜 pipeline 容易除錯
	cursor, err := r.collection.Find(ctx, bson.M{"is_ignored": false}, options.Find().SetProjection(bson.M{
		"status": 1, "days_remaining": 1, "issuer": 1, "is_match": 1, // [新增]
	}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	type miniCert struct {
		Status        string `bson:"status"`
		DaysRemaining int    `bson:"days_remaining"`
		Issuer        string `bson:"issuer"`
		IsMatch       bool   `bson:"is_match"`
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

		// if !c.IsMatch && c.Status != "unresolvable" {
		// 	stats.MismatchCount++ // 需確保 DashboardStats 結構有此欄位
		// }
		if !c.IsMatch && c.Status != domain.StatusUnresolvable && c.Status != domain.StatusConnectionError {
			stats.MismatchCount++
		}

		if c.Status == "connection_error" {
			stats.ConnectionError++ // 確保 domain.DashboardStats 有此欄位
		}
		// 統計過期區間
		// 注意：只有 active/warning 的才需要算剩餘天數
		// [修改重點] 3. 統計到期區間 (互斥邏輯)
		// 排除掉 Unresolvable, Pending, 以及已經 Expired 的
		// if c.Status != "unresolvable" && c.Status != "pending" && c.Status != "expired"
		if c.Status != domain.StatusUnresolvable &&
			c.Status != domain.StatusPending &&
			c.Status != domain.StatusExpired &&
			c.Status != domain.StatusConnectionError {

			if c.DaysRemaining < 16 {
				// 危險區：0 ~ 6 天
				stats.ExpiryCounts["d15"]++
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
// func (r *mongoDomainRepo) Upsert(ctx context.Context, cert domain.SSLCertificate) error {
// 	filter := bson.M{
// 		"domain_name":  cert.DomainName,
// 		"cf_record_id": cert.CFRecordID,
// 	}

// 	update := bson.M{
// 		"$set": bson.M{
// 			"cf_zone_id":      cert.CFZoneID,
// 			"zone_name":       cert.ZoneName,
// 			"is_proxied":      cert.IsProxied,
// 			"status":          cert.Status,
// 			"cf_record_type":  cert.CFRecordType,
// 			"cf_origin_value": cert.CFOriginValue,
// 			// 注意：我們不更新 "is_ignored" 和 "auto_renew"，以免覆蓋使用者設定
// 		},
// 		"$setOnInsert": bson.M{
// 			"created_at": time.Now(),
// 			"is_ignored": false, // 預設值
// 		},
// 	}

// 	opts := options.Update().SetUpsert(true)
// 	_, err := r.collection.UpdateOne(ctx, filter, update, opts)
// 	return err
// }

// Upsert: 根據 DomainName 和 CFRecordID 判斷，有則更新，無則新增
// [修正] 必須包含所有 SSL 欄位，否則 CronService 同步時會遺失掃描結果
func (r *mongoDomainRepo) Upsert(ctx context.Context, cert domain.SSLCertificate) error {
	filter := bson.M{
		"domain_name":  cert.DomainName,
		"cf_record_id": cert.CFRecordID,
	}

	update := bson.M{
		"$set": bson.M{
			// --- Cloudflare 資訊 ---
			"cf_zone_id":      cert.CFZoneID,
			"zone_name":       cert.ZoneName,
			"is_proxied":      cert.IsProxied,
			"cf_record_type":  cert.CFRecordType,
			"cf_origin_value": cert.CFOriginValue,
			"port":            cert.Port, // 確保 Port 也被更新
			"cf_comment":      cert.CFComment,

			// --- 系統狀態 ---
			"status":          cert.Status,
			"last_check_time": time.Now(), // 確保時間更新
			"error_msg":       cert.ErrorMsg,

			// --- [關鍵新增] SSL 憑證資訊 (原本漏掉了這些) ---
			"issuer":           cert.Issuer,
			"not_before":       cert.NotBefore,
			"not_after":        cert.NotAfter,
			"days_remaining":   cert.DaysRemaining,
			"sans":             cert.SANs,
			"tls_version":      cert.TLSVersion,
			"http_status_code": cert.HTTPStatusCode,
			"latency":          cert.Latency,
			"is_match":         cert.IsMatch,

			// --- [關鍵新增] 網路/WHOIS 資訊 ---
			"domain_expiry_date": cert.DomainExpiryDate,
			"domain_days_left":   cert.DomainDaysLeft,
			"resolved_ips":       cert.ResolvedIPs,
			"resolved_record":    cert.ResolvedRecord,

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
func (r *mongoDomainRepo) List(ctx context.Context, page, pageSize int64, sortBy, search, statusFilter, proxiedFilter, ignoredFilter, zoneFilter string) ([]domain.SSLCertificate, int64, error) {
	skip := (page - 1) * pageSize
	// 建構過濾條件
	filter := bson.M{}

	// [新增] 搜尋邏輯 (模糊搜尋 域名 或 解析紀錄)
	if search != "" {
		filter["$or"] = []bson.M{
			{"domain_name": primitive.Regex{Pattern: search, Options: "i"}},     // 忽略大小寫
			{"resolved_record": primitive.Regex{Pattern: search, Options: "i"}}, // 也可以搜尋 IP
			{"zone_name": primitive.Regex{Pattern: search, Options: "i"}},
		}
	}
	// 1. [新增] 主域名過濾
	if zoneFilter != "" {
		filter["zone_name"] = zoneFilter
	}

	// [修改] 狀態篩選邏輯
	if statusFilter != "" {
		switch statusFilter {
		case "active_only":
			filter["status"] = bson.M{"$ne": "unresolvable"}
		case "mismatch":
			// [新增] 篩選憑證不符 (且不是忽略或無法解析的)
			filter["is_match"] = false
			filter["is_ignored"] = false
			filter["status"] = bson.M{"$ne": "unresolvable"}
		default:
			// 包含 active, expired, warning, pending, unresolvable
			filter["status"] = statusFilter
		}
	}

	// 3. [新增] Proxy 過濾
	if proxiedFilter == "true" {
		filter["is_proxied"] = true
	} else if proxiedFilter == "false" {
		filter["is_proxied"] = false
	}

	// 4. [修正] 忽略狀態過濾
	if ignoredFilter == "true" {
		// 模式 A: 只顯示「已忽略」的域名
		filter["is_ignored"] = true
	} else if ignoredFilter == "false" || ignoredFilter == "" {
		// 模式 B: 只顯示「監控中」的域名 (預設)
		filter["is_ignored"] = false
	}
	// 註：如果 ignoredFilter 既不是 true 也不是 false (例如特殊值 "all")，則顯示全部，不加 filter

	// 排序設定
	// 預設排序：按建立時間或 ID 倒序
	// sortOpts := bson.D{{Key: "_id", Value: -1}}
	var sortOpts bson.D = bson.D{{Key: "_id", Value: -1}}

	zeroDate := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

	// [輔助判斷] 是否正在查看「已忽略」列表
	// 如果是查看忽略列表，我們就不應該過濾掉日期為空的資料，因為使用者就是要看這些被忽略的東西
	isViewingIgnored := (ignoredFilter == "true")

	switch sortBy {
	// 1. SSL 到期日 (關鍵修正)
	case "expiry_asc":
		sortOpts = bson.D{{Key: "not_after", Value: 1}}
		// [修正邏輯]
		// 只有在:
		// 1. 沒有搜尋關鍵字
		// 2. 沒有指定狀態
		// 3. 且 "不是" 在查看已忽略列表時
		// 才過濾掉 0001-01-01 的資料。
		if search == "" && statusFilter == "" && !isViewingIgnored {
			filter["not_after"] = bson.M{"$gt": zeroDate}
		}

	case "expiry_desc":
		sortOpts = bson.D{{Key: "not_after", Value: -1}}
		// 倒序 (最晚過期) 通常不需要過濾，因為 2030 年會排在前面，0001 年會排在最後面，不影響閱讀
		// 但為了乾淨，你也可以選擇過濾：
		// filter["not_after"] = bson.M{"$gt": zeroDate}

	// 2. 網域註冊到期日 (關鍵修正)
	case "domain_expiry_asc":
		sortOpts = bson.D{{Key: "domain_expiry_date", Value: 1}}
		// [修正邏輯] 同上
		if search == "" && statusFilter == "" && !isViewingIgnored {
			filter["domain_expiry_date"] = bson.M{"$gt": zeroDate}
		}
	case "domain_expiry_desc":
		sortOpts = bson.D{{Key: "domain_expiry_date", Value: -1}}

	// 3. 剩餘天數
	case "days_remaining_asc":
		sortOpts = bson.D{{Key: "days_remaining", Value: 1}}
		// [修正邏輯] 同上
		if search == "" && statusFilter == "" && !isViewingIgnored {
			filter["not_after"] = bson.M{"$gt": zeroDate}
		}

	case "days_remaining_desc":
		sortOpts = bson.D{{Key: "days_remaining", Value: -1}}

	// 4. 上次檢查時間
	case "check_time_asc":
		sortOpts = bson.D{{Key: "last_check_time", Value: 1}}
		// 上次檢查時間通常需要過濾，不然會看到很多 1970 年的
		if search == "" && statusFilter == "" && !isViewingIgnored {
			filter["last_check_time"] = bson.M{"$gt": zeroDate}
		}
	case "check_time_desc":
		sortOpts = bson.D{{Key: "last_check_time", Value: -1}}
	}

	logrus.Infof("🔍 Query Sort: %s | Applied Mongo Sort: %v\n", sortBy, sortOpts)

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
			"issuer":             cert.Issuer,
			"not_before":         cert.NotBefore,
			"not_after":          cert.NotAfter,
			"days_remaining":     cert.DaysRemaining,
			"status":             cert.Status,
			"error_msg":          cert.ErrorMsg,
			"sans":               cert.SANs,
			"port":               cert.Port,
			"last_check_time":    time.Now(),
			"tls_version":        cert.TLSVersion,
			"http_status_code":   cert.HTTPStatusCode,
			"latency":            cert.Latency,
			"domain_expiry_date": cert.DomainExpiryDate,
			"domain_days_left":   cert.DomainDaysLeft,
			"resolved_ips":       cert.ResolvedIPs,
			"resolved_record":    cert.ResolvedRecord,
			"is_match":           cert.IsMatch,
			"cf_record_type":     cert.CFRecordType, // [新增]
			"cf_origin_value":    cert.CFOriginValue,
			"cf_comment":         cert.CFComment,
		},
	}

	_, err := r.collection.UpdateOne(ctx, filter, update)
	return err
}

// 3. [新增] 實作 UpdateSettings
func (r *mongoDomainRepo) UpdateSettings(ctx context.Context, id string, isIgnored bool, port int) error {
	oid, _ := primitive.ObjectIDFromHex(id)
	filter := bson.M{"_id": oid}
	update := bson.M{
		"$set": bson.M{"is_ignored": isIgnored, "port": port},
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

// 實作
func (r *mongoDomainRepo) UpdateAcmeData(ctx context.Context, email, privateKey, regData string) error {
	coll := r.collection.Database().Collection("settings")
	update := bson.M{"$set": bson.M{}}
	if email != "" {
		update["$set"].(bson.M)["acme_email"] = email
	}
	if privateKey != "" {
		update["$set"].(bson.M)["acme_private_key"] = privateKey
	}
	if regData != "" {
		update["$set"].(bson.M)["acme_reg_data"] = regData
	}

	_, err := coll.UpdateOne(ctx, bson.M{}, update, options.Update().SetUpsert(true))
	return err
}

func (r *mongoDomainRepo) BatchUpdateSettings(ctx context.Context, ids []primitive.ObjectID, isIgnored bool) error {
	filter := bson.M{"_id": bson.M{"$in": ids}}
	update := bson.M{"$set": bson.M{"is_ignored": isIgnored}}

	_, err := r.collection.UpdateMany(ctx, filter, update)
	return err
}

// [新增] 實作 Create
func (r *mongoDomainRepo) Create(ctx context.Context, cert domain.SSLCertificate) error {
	// 如果沒有 ID，生成一個
	if cert.ID.IsZero() {
		cert.ID = primitive.NewObjectID()
	}
	// 寫入資料庫
	_, err := r.collection.InsertOne(ctx, cert)
	return err
}

// [新增] 實作 GetByID
func (r *mongoDomainRepo) GetByID(ctx context.Context, id primitive.ObjectID) (*domain.SSLCertificate, error) {
	var cert domain.SSLCertificate
	filter := bson.M{"_id": id}

	err := r.collection.FindOne(ctx, filter).Decode(&cert)
	if err != nil {
		return nil, err
	}
	return &cert, nil
}

// [新增] 實作 Delete
func (r *mongoDomainRepo) Delete(ctx context.Context, id primitive.ObjectID) error {
	filter := bson.M{"_id": id}
	_, err := r.collection.DeleteOne(ctx, filter)
	return err
}

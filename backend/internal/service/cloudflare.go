package service

import (
	"cert-manager/internal/domain"
	"cert-manager/internal/repository"
	"context"
	"fmt"
	"time"

	"github.com/cloudflare/cloudflare-go"
	"github.com/likexian/whois"
	whoisparser "github.com/likexian/whois-parser"
	"github.com/sirupsen/logrus"
)

// 常數定義：方便統一調整參數
const (
	cfPageSize       = 100
	cfRateLimitSleep = 1000 * time.Millisecond // 避免觸發 API 限制
)

type CloudflareService struct {
	APIToken string
	Repo     repository.DomainRepository
}

func NewCloudflareService(token string, repo repository.DomainRepository) *CloudflareService {
	return &CloudflareService{APIToken: token, Repo: repo}
}

// =============================================================================
// Public Methods (業務入口)
// =============================================================================

// FetchDomains 從 Cloudflare 抓取所有 Zone 下的 A 紀錄和 CNAME，並結合 WHOIS 資訊
// func (s *CloudflareService) FetchDomains(ctx context.Context, outputChan chan<- domain.SSLCertificate) error {
// 	logrus.Info("🚀 [Cloudflare] 開始執行 FetchDomains (串流模式)...")

// 	api, err := s.getAPIClient()
// 	if err != nil {
// 		return err
// 	}

// 	// 1. 獲取所有 Zones
// 	zones, err := s.listAllZones(ctx, api)
// 	if err != nil {
// 		return err
// 	}
// 	logrus.Infof("✅ [Cloudflare] 取得 Zone 列表成功，共 %d 個 Zone", len(zones))

// 	// var allDomains []domain.SSLCertificate

// 	// 2. 遍歷每個 Zone 進行處理
// 	for i, zone := range zones {
// 		// 每次處理完一個 Zone，休息一下避免 Rate Limit
// 		if i > 0 {
// 			time.Sleep(cfRateLimitSleep)
// 		}

// 		logrus.Infof("🔍 [%d/%d] 正在掃描 Zone: %s (ID: %s)", i+1, len(zones), zone.Name, zone.ID)

// 		// 處理單一 Zone 的所有邏輯 (Whois + DNS Records)
// 		zoneDomains := s.processZone(ctx, api, zone)
// 		// [關鍵] 將抓到的域名立即推送到通道
// 		for _, d := range zoneDomains {
// 			select {
// 			case <-ctx.Done():
// 				return ctx.Err()
// 			case outputChan <- d: // <--- 這裡！一抓到就丟給 CronService 去掃描
// 			}
// 		}
// 		// allDomains = append(allDomains, zoneDomains...)
// 	}
// 	logrus.Info("🏁 [Cloudflare] 所有 Zone 抓取完畢，關閉資料通道")
// 	// logrus.Infof("🏁 [Cloudflare] 掃描完成，總計處理 %d 個子域名", len(allDomains))
// 	return nil
// }

func (s *CloudflareService) FetchDomains(ctx context.Context, outputChan chan<- domain.SSLCertificate) error {
	logrus.Info("🚀 [Cloudflare] 開始執行 FetchDomains (串流模式)...")

	api, err := s.getAPIClient()
	if err != nil {
		return err
	}
	zoneID := "da4987ebbc2c7fd3b1e4a15f0d04320d"
	// 1. 獲取所有 Zones
	zone, err := api.ZoneDetails(ctx, zoneID)
	if err != nil {
		return fmt.Errorf("獲取 Zone %s 失敗: %w", zoneID, err)
	}

	// var allDomains []domain.SSLCertificate

	// 2. 遍歷每個 Zone 進行處理
	zoneDomains := s.processZone(ctx, api, zone)
	for _, d := range zoneDomains {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case outputChan <- d: // <--- 這裡！一抓到就丟給 CronService 去掃描
		}
	}
	logrus.Info("🏁 [Cloudflare] 所有 Zone 抓取完畢，關閉資料通道")
	// logrus.Infof("🏁 [Cloudflare] 掃描完成，總計處理 %d 個子域名", len(allDomains))
	return nil
}

// GetSingleRecord 從 Cloudflare 獲取單一域名的最新設定 (用於比對更新)
func (s *CloudflareService) GetSingleRecord(ctx context.Context, zoneID, recordID string) (domain.SSLCertificate, error) {
	api, err := s.getAPIClient()
	if err != nil {
		return domain.SSLCertificate{}, err
	}

	record, err := api.GetDNSRecord(ctx, cloudflare.ZoneIdentifier(zoneID), recordID)
	if err != nil {
		return domain.SSLCertificate{}, err
	}

	// 回傳簡化的物件，僅包含需要比對的欄位
	return domain.SSLCertificate{
		CFOriginValue: record.Content,
		CFRecordType:  record.Type,
		IsProxied:     *record.Proxied,
	}, nil
}

// =============================================================================
// Private Methods (核心邏輯封裝)
// =============================================================================

// processZone 處理單一 Zone 的完整流程：查詢 WHOIS -> 抓取 Records -> 轉換資料
func (s *CloudflareService) processZone(ctx context.Context, api *cloudflare.API, zone cloudflare.Zone) []domain.SSLCertificate {
	var results []domain.SSLCertificate

	// A. 查詢 Zone (根域名) 的 WHOIS
	expiryDate, daysLeft, err := s.fetchZoneWhois(zone.Name)
	if err != nil {
		logrus.Warnf("   ⚠️ Zone WHOIS 查詢失敗 %s: %v (子域名將無到期日資料)", zone.Name, err)
	} else {
		logrus.Infof("   📅 Zone 到期日: %s (剩餘 %d 天)", expiryDate.Format("2006-01-02"), daysLeft)
	}

	// B. 分頁獲取所有 DNS 紀錄
	records, err := s.fetchAllZoneRecords(ctx, api, zone)
	if err != nil {
		logrus.Errorf("❌ 無法獲取 Zone %s 的紀錄: %v", zone.Name, err)
		return nil
	}
	logrus.Debugf("   -> Zone %s 找到 %d 筆紀錄", zone.Name, len(records))

	// C. 過濾並轉換為 Domain Model
	for _, record := range records {
		if !isValidRecordType(record.Type) {
			continue
		}

		// =================================================================
		// [關鍵修正] 在寫入 DB 之前，先檢查是否應該略過
		// =================================================================
		if shouldSkipDomain(record.Name) {
			logrus.Debugf("      🚫 [Skip] 略過不需要的域名: %s", record.Name)
			continue
		}
		// =================================================================

		logrus.Infof("      -> 發現子域名: [%s] %s (Target: %s)", record.Type, record.Name, record.Content)

		cert := s.mapRecordToDomain(zone, record, expiryDate, daysLeft)

		// 2. [新增] 立即寫入資料庫 (Pending)
		// 使用 Upsert: 如果已存在則更新 (例如更新 Proxy 狀態)，不存在則新增
		// 注意：這裡只會寫入 Cloudflare 的基本資訊，Status 預設為 "pending"
		// 我們需要小心不要覆蓋掉已經是 "active" 的狀態

		// 先查一下舊資料，避免把已經掃描好的 active 覆蓋回 pending
		// (雖然這是 Sync 流程，覆蓋回 pending 等待重掃也是合理的，但為了體驗，我們可以做個檢查)
		// 為了效能，這裡直接用 Upsert，但在 Upsert 實作層面，建議只更新 "非 SSL 相關" 欄位，或者我們接受它短暫變回 pending

		// 簡單策略：直接 Upsert，讓它變成 Pending。這樣使用者知道「正在重新同步中」。
		// 或者，您可以在這裡只做 "Insert if not exists"。

		// 為了達到您的需求「一發現就進入 Pending」，我們執行 Upsert
		if err := s.Repo.Upsert(ctx, cert); err != nil {
			logrus.Errorf("      ❌ 寫入 Pending 失敗: %v", err)
		} else {
			logrus.Debugf("      ✅ 已寫入 Pending: %s", cert.DomainName)
		}
		results = append(results, cert)
	}

	if zone.Status != "active" {
		logrus.Warnf("發現非 Active 域名: %s (Status: %s)", zone.Name, zone.Status)
	}

	return results
}

// fetchAllZoneRecords 處理 Cloudflare 分頁邏輯，抓取該 Zone 下所有紀錄
func (s *CloudflareService) fetchAllZoneRecords(ctx context.Context, api *cloudflare.API, zone cloudflare.Zone) ([]cloudflare.DNSRecord, error) {
	var allRecords []cloudflare.DNSRecord
	page := 1

	logrus.Infof("   📥 [Zone: %s] 開始抓取 DNS 紀錄...", zone.Name)

	for {
		params := cloudflare.ListDNSRecordsParams{
			ResultInfo: cloudflare.ResultInfo{
				Page:    page,
				PerPage: cfPageSize,
			},
		}

		records, info, err := api.ListDNSRecords(ctx, cloudflare.ZoneIdentifier(zone.ID), params)
		if err != nil {
			logrus.Errorf("   ❌ [Zone: %s] 第 %d 頁抓取失敗: %v", zone.Name, page, err)
			return nil, err
		}

		count := len(records)
		allRecords = append(allRecords, records...)

		logrus.Infof("      -> [Page %d/%d] 抓取 %d 筆 (累計: %d)",
			info.Page, info.TotalPages, count, len(allRecords))

		if info.Page >= info.TotalPages {
			break
		}
		page++

		time.Sleep(200 * time.Millisecond)
	}
	logrus.Infof("   ✅ [Zone: %s] 抓取完成，共 %d 筆紀錄", zone.Name, len(allRecords))
	return allRecords, nil
}

// listAllZones 封裝獲取 Zone 列表的邏輯
func (s *CloudflareService) listAllZones(ctx context.Context, api *cloudflare.API) ([]cloudflare.Zone, error) {
	logrus.Info("📡 [Cloudflare] 正在請求 ListZones API...")
	zones, err := api.ListZones(ctx)
	if err != nil {
		logrus.Errorf("❌ [Cloudflare] ListZones 請求失敗: %v", err)
		return nil, err
	}
	return zones, nil
}

// fetchZoneWhois 查詢並解析 WHOIS 時間
func (s *CloudflareService) fetchZoneWhois(domainName string) (time.Time, int, error) {
	raw, err := whois.Whois(domainName)
	if err != nil {
		return time.Time{}, 0, err
	}

	result, err := whoisparser.Parse(raw)
	if err != nil {
		return time.Time{}, 0, err
	}

	if result.Domain.ExpirationDate == "" {
		return time.Time{}, 0, fmt.Errorf("no expiration date found")
	}

	return s.parseWhoisTime(result.Domain.ExpirationDate)
}

// mapRecordToDomain 將 Cloudflare 原始資料映射為內部資料結構
func (s *CloudflareService) mapRecordToDomain(zone cloudflare.Zone, record cloudflare.DNSRecord, expiryDate time.Time, daysLeft int) domain.SSLCertificate {
	return domain.SSLCertificate{
		DomainName:       record.Name,
		CFZoneID:         zone.ID,
		ZoneName:         zone.Name,
		CFRecordID:       record.ID,
		IsProxied:        *record.Proxied,
		DomainExpiryDate: expiryDate,
		DomainDaysLeft:   daysLeft,
		CFOriginValue:    record.Content,
		CFRecordType:     record.Type,
		CFComment:        record.Comment,
		IsIgnored:        false,
		Status:           "pending",
	}
}

// =============================================================================
// Helper Functions (工具與底層邏輯)
// =============================================================================

func (s *CloudflareService) getAPIClient() (*cloudflare.API, error) {
	api, err := cloudflare.NewWithAPIToken(s.APIToken)
	if err != nil {
		logrus.Errorf("❌ [Cloudflare] API Client 初始化失敗: %v", err)
		return nil, err
	}
	return api, nil
}

// parseWhoisTime 嘗試多種格式解析時間
func (s *CloudflareService) parseWhoisTime(dateStr string) (time.Time, int, error) {
	var expiryTime time.Time

	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05.00Z",
		"2006-01-02",
	}

	for _, f := range formats {
		if t, e := time.Parse(f, dateStr); e == nil {
			expiryTime = t
			break
		}
	}

	if expiryTime.IsZero() {
		return time.Time{}, 0, fmt.Errorf("date parse fail: %s", dateStr)
	}

	daysLeft := int(time.Until(expiryTime).Hours() / 24)
	return expiryTime, daysLeft, nil
}

func isValidRecordType(recordType string) bool {
	return recordType == "A" || recordType == "CNAME"
}

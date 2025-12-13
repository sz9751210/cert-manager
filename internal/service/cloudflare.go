package service

import (
	"cert-manager/internal/domain"
	"context"

	"github.com/cloudflare/cloudflare-go"
	"github.com/sirupsen/logrus"
)

type CloudflareService struct {
	APIToken string
}

func NewCloudflareService(token string) *CloudflareService {
	return &CloudflareService{APIToken: token}
}

// FetchDomains 從 Cloudflare 抓取所有 Zone 下的 A 紀錄和 CNAME
func (s *CloudflareService) FetchDomains(ctx context.Context) ([]domain.SSLCertificate, error) {
	api, err := cloudflare.NewWithAPIToken(s.APIToken)
	if err != nil {
		return nil, err
	}

	// 1. 獲取所有 Zones
	zones, err := api.ListZones(ctx)
	if err != nil {
		return nil, err
	}

	var allDomains []domain.SSLCertificate

	for _, zone := range zones {
		logrus.Infof("正在掃描 Zone: %s", zone.Name)

		// 2. 獲取 DNS 紀錄
		// 修正：使用新版 SDK 的參數名稱 ListDNSRecordsParams
		params := cloudflare.ListDNSRecordsParams{}

		records, _, err := api.ListDNSRecords(ctx, cloudflare.ZoneIdentifier(zone.ID), params)
		if err != nil {
			logrus.Errorf("無法獲取 Zone %s 的紀錄: %v", zone.Name, err)
			continue
		}

		for _, record := range records {
			// 過濾：只監控 A 和 CNAME 紀錄
			if record.Type != "A" && record.Type != "CNAME" {
				continue
			}

			// 轉換為我們的 Domain Model
			cert := domain.SSLCertificate{
				DomainName: record.Name,
				CFZoneID:   zone.ID,
				ZoneName:   zone.Name, // [新增] 寫入主域名 (例如 example.com)
				CFRecordID: record.ID,
				IsProxied:  *record.Proxied,
				IsIgnored:  false,
				Status:     "pending",
			}
			allDomains = append(allDomains, cert)
		}
	}

	logrus.Infof("共掃描到 %d 個子域名", len(allDomains))
	return allDomains, nil
}

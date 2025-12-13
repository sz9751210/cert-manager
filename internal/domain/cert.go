package domain

import (
	"go.mongodb.org/mongo-driver/bson/primitive"
	"time"
)

// 定義常數避免打錯字
const (
	StatusActive       = "active"
	StatusUnresolvable = "unresolvable" // 無法解析/內網
	StatusExpired      = "expired"
	StatusWarning      = "warning"
)

type SSLCertificate struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	DomainName string             `bson:"domain_name" json:"domain_name"`

	// Cloudflare 資訊 (用於排序和識別 Proxy)
	CFZoneID   string `bson:"cf_zone_id" json:"cf_zone_id"`
	ZoneName   string `bson:"zone_name" json:"zone_name"`
	CFRecordID string `bson:"cf_record_id" json:"cf_record_id"`
	IsProxied  bool   `bson:"is_proxied" json:"is_proxied"` // 小橘雲是否開啟

	// 監控設定
	IsIgnored bool `bson:"is_ignored" json:"is_ignored"` // 開關檢查按鈕
	AutoRenew bool `bson:"auto_renew" json:"auto_renew"` // Let's Encrypt 預留欄位

	// 憑證狀態
	Issuer        string    `bson:"issuer" json:"issuer"`
	NotBefore     time.Time `bson:"not_before" json:"not_before"`
	NotAfter      time.Time `bson:"not_after" json:"not_after"` // 過期日 (用於排序)
	DaysRemaining int       `bson:"days_remaining" json:"days_remaining"`

	// 系統欄位
	LastCheckTime time.Time `bson:"last_check_time" json:"last_check_time"`
	Status        string    `bson:"status" json:"status"`
	ErrorMsg      string    `bson:"error_msg,omitempty" json:"error_msg"`
}

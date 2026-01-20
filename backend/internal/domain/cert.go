package domain

import (
	"go.mongodb.org/mongo-driver/bson/primitive"
	"time"
)

// 定義常數避免打錯字
const (
	StatusActive          = "active"
	StatusUnresolvable    = "unresolvable" // 無法解析/內網
	StatusExpired         = "expired"
	StatusWarning         = "warning"
	StatusConnectionError = "connection_error"
	StatusPending         = "pending"
)

type SSLCertificate struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	DomainName string             `bson:"domain_name" json:"domain_name"`

	// Cloudflare 資訊 (用於排序和識別 Proxy)
	CFZoneID   string `bson:"cf_zone_id" json:"cf_zone_id"`
	ZoneName   string `bson:"zone_name" json:"zone_name"`
	CFRecordID string `bson:"cf_record_id" json:"cf_record_id"`
	IsProxied  bool   `bson:"is_proxied" json:"is_proxied"` // 小橘雲是否開啟
	Port       int    `bson:"port" json:"port"`

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
	// [新增] 記錄上次發送告警的時間，避免頻繁轟炸
	LastAlertTime time.Time `bson:"last_alert_time" json:"last_alert_time"`

	// [新增] 憑證包含的所有域名 (Subject Alternative Names)
	SANs []string `bson:"sans" json:"sans"`

	TLSVersion     string `bson:"tls_version" json:"tls_version"`           // e.g. "TLS 1.3"
	HTTPStatusCode int    `bson:"http_status_code" json:"http_status_code"` // e.g. 200, 404, 500
	Latency        int64  `bson:"latency" json:"latency"`                   // 毫秒 (ms)

	// [新增] 網域註冊資訊
	DomainExpiryDate time.Time `bson:"domain_expiry_date" json:"domain_expiry_date"`
	DomainDaysLeft   int       `bson:"domain_days_left" json:"domain_days_left"`

	ResolvedIPs []string `bson:"resolved_ips" json:"resolved_ips"`
	// [修改] 解析紀錄 (可能是 IP 列表，也可能是 CNAME 域名)
	ResolvedRecord string `bson:"resolved_record" json:"resolved_record"`
	CFRecordType   string `bson:"cf_record_type" json:"cf_record_type"`
	CFOriginValue  string `bson:"cf_origin_value" json:"cf_origin_value"`
	CFComment      string `bson:"cf_comment" json:"cf_comment"`
	
	// ResolvedIP string `bson:"resolved_ip" json:"resolved_ip"` // [新增] 解析後的 IP
	// true = 匹配, false = 不匹配 (例如 example.com 用了 google.com 的憑證)
	IsMatch bool `bson:"is_match" json:"is_match"`
}

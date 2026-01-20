package domain

type DashboardStats struct {
	TotalDomains    int64          `json:"total_domains"`
	TotalZones      int            `json:"total_zones"`     // 主域名總數
	IgnoredDomains  int            `json:"ignored_domains"` // 暫停監控總數
	ConnectionError int            `json:"connection_error"`
	StatusCounts    map[string]int `json:"status_counts"` // e.g. "active": 50, "expired": 2
	ExpiryCounts    map[string]int `json:"expiry_counts"` // e.g. "<7": 1, "<30": 5
	IssuerCounts    map[string]int `json:"issuer_counts"` // e.g. "R3": 40
	MismatchCount   int            `json:"mismatch_count"`
}

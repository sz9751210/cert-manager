package api

import (
	"cert-manager/internal/domain"
	"cert-manager/internal/repository"
	"cert-manager/internal/service"
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type DomainHandler struct {
	Repo      repository.DomainRepository
	CFService *service.CloudflareService
	Scanner   *service.ScannerService
	Notifier  *service.NotifierService
	Acme      *service.AcmeService
}

// [新增] 定義請求結構
type UpdateSettingsRequest struct {
	IsIgnored bool `json:"is_ignored"`
}

func NewDomainHandler(r repository.DomainRepository, c *service.CloudflareService, s *service.ScannerService, n *service.NotifierService, acme *service.AcmeService) *DomainHandler {
	return &DomainHandler{Repo: r, CFService: c, Scanner: s, Notifier: n, Acme: acme}
}

// SyncDomains godoc
// @Summary 手動觸發 Cloudflare 同步
// @Tags domains
// @Accept json
// @Produce json
// @Success 200 {object} map[string]string
// @Router /api/v1/domains/sync [post]
func (h *DomainHandler) SyncDomains(c *gin.Context) {
	// 呼叫 Service 去抓 Cloudflare 資料
	domains, err := h.CFService.FetchDomains(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 寫入資料庫
	count := 0
	for _, d := range domains {
		if err := h.Repo.Upsert(c.Request.Context(), d); err == nil {
			count++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "同步完成",
		"total":   count,
	})
}

// GetDomains godoc
// @Summary 獲取域名列表 (支援分頁與排序)
// @Param page query int false "頁碼"
// @Param limit query int false "每頁數量"
// @Param sort query string false "排序欄位 (expiry_asc)"
// @Router /api/v1/domains [get]
func (h *DomainHandler) GetDomains(c *gin.Context) {
	page, _ := strconv.ParseInt(c.DefaultQuery("page", "1"), 10, 64)
	limit, _ := strconv.ParseInt(c.DefaultQuery("limit", "10"), 10, 64)
	sort := c.Query("sort")
	status := c.Query("status")   // 讀取狀態參數
	proxied := c.Query("proxied") // 读取 proxied 参数
	ignored := c.Query("ignored") // 读取 ignored 参数 (true/false)
	zone := c.Query("zone")
	// 將 status 傳入 List
	domains, total, err := h.Repo.List(c.Request.Context(), page, limit, sort, status, proxied, ignored, zone)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  domains,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// 2. [新增] GetZones API
func (h *DomainHandler) GetZones(c *gin.Context) {
	zones, err := h.Repo.GetUniqueZones(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": zones})
}

// ScanDomains 手動觸發 SSL 掃描
// @Router /api/v1/domains/scan [post]
func (h *DomainHandler) ScanDomains(c *gin.Context) {
	// 在背景執行掃描，不阻塞 HTTP Response
	go func() {
		if err := h.Scanner.ScanAll(context.Background()); err != nil {
			logrus.Errorf("背景掃描失敗: %v", err)
		}
	}()

	c.JSON(http.StatusOK, gin.H{"message": "掃描任務已在背景啟動"})
}

// [新增] UpdateSettings Handler
// @Router /api/v1/domains/:id/settings [patch]
func (h *DomainHandler) UpdateSettings(c *gin.Context) {
	id := c.Param("id")
	var req UpdateSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid body"})
		return
	}

	if err := h.Repo.UpdateSettings(c.Request.Context(), id, req.IsIgnored); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "設定已更新"})
}

// [API] 獲取設定
func (h *DomainHandler) GetSettings(c *gin.Context) {
	settings, err := h.Repo.GetSettings(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": settings})
}

// [API] 儲存設定
func (h *DomainHandler) SaveSettings(c *gin.Context) {
	var settings domain.NotificationSettings
	if err := c.ShouldBindJSON(&settings); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}
	if err := h.Repo.SaveSettings(c.Request.Context(), settings); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "設定已儲存"})
}

func (h *DomainHandler) TestNotification(c *gin.Context) {
	var settings domain.NotificationSettings
	// 前端直接把表單填寫的設定傳過來測試，而不是只傳 URL
	if err := c.ShouldBindJSON(&settings); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}

	if err := h.Notifier.SendTestMessage(c.Request.Context(), settings); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "測試訊息發送成功"})
}

// GetStatistics 獲取儀表板數據
func (h *DomainHandler) GetStatistics(c *gin.Context) {
	stats, err := h.Repo.GetStatistics(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": stats})
}

// [API] 觸發續簽
func (h *DomainHandler) RenewCertificate(c *gin.Context) {
	c.Param("id")
	// 先查出域名
	// (這裡需要 Repo 支援 GetByID，或者我們先簡單用參數傳 DomainName)
	// 為了安全，應該傳 ID 然後由後端查 DomainName
	// 這裡假設前端傳 JSON body: { "domain": "example.com" }

	var req struct {
		Domain string `json:"domain"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要 domain 參數"})
		return
	}

	// 非同步執行，因為申請憑證可能要 10-30 秒
	go func() {
		if err := h.Acme.RenewCertificate(context.Background(), req.Domain); err != nil {
			logrus.Errorf("續簽失敗 %s: %v", req.Domain, err)
		} else {
			logrus.Infof("續簽成功 %s", req.Domain)
		}
	}()

	c.JSON(http.StatusOK, gin.H{"message": "續簽請求已排入背景處理"})
}

// [API] 儲存 ACME Email
func (h *DomainHandler) SaveAcmeSettings(c *gin.Context) {
	var req struct {
		Email string `json:"email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要 email"})
		return
	}

	if err := h.Repo.UpdateAcmeData(c.Request.Context(), req.Email, "", ""); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Let's Encrypt Email 已儲存"})
}

// [API] 批量更新設定
func (h *DomainHandler) BatchUpdateSettings(c *gin.Context) {
	var req struct {
		IDs       []string `json:"ids"`
		IsIgnored bool     `json:"is_ignored"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}

	// 轉換 string ID 為 ObjectID
	var objectIDs []primitive.ObjectID
	for _, id := range req.IDs {
		if oid, err := primitive.ObjectIDFromHex(id); err == nil {
			objectIDs = append(objectIDs, oid)
		}
	}

	if err := h.Repo.BatchUpdateSettings(c.Request.Context(), objectIDs, req.IsIgnored); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "批量更新成功"})
}

// [API] 匯出 CSV
func (h *DomainHandler) ExportDomains(c *gin.Context) {
	// 1. 撈取所有資料 (依照目前的過濾條件，這裡簡化為撈全部 active 的)
	// 實務上您可能需要把 List 的參數都傳進來做篩選
	domains, _, err := h.Repo.List(c.Request.Context(), 1, 10000, "expiry_asc", "", "", "false", "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 2. 設定 Response Header 讓瀏覽器下載
	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", "attachment;filename=domains_report.csv")

	// 3. 寫入 CSV
	writer := csv.NewWriter(c.Writer)
	// 寫入 Header
	writer.Write([]string{"Domain", "Issuer", "Expiry Date", "Days Left", "Status", "Proxy", "Zone"})

	// 寫入資料
	for _, d := range domains {
		writer.Write([]string{
			d.DomainName,
			d.Issuer,
			d.NotAfter.Format("2006-01-02"),
			fmt.Sprintf("%d", d.DaysRemaining),
			string(d.Status),
			fmt.Sprintf("%v", d.IsProxied),
			d.ZoneName,
		})
	}
	writer.Flush()
}

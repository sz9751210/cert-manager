package api

import (
	"cert-manager/internal/domain"
	"cert-manager/internal/repository"
	"cert-manager/internal/service"
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

type DomainHandler struct {
	Repo      repository.DomainRepository
	CFService *service.CloudflareService
	Scanner   *service.ScannerService
	Notifier  *service.NotifierService
}

// [新增] 定義請求結構
type UpdateSettingsRequest struct {
	IsIgnored bool `json:"is_ignored"`
}

func NewDomainHandler(r repository.DomainRepository, c *service.CloudflareService, s *service.ScannerService, n *service.NotifierService) *DomainHandler {
	return &DomainHandler{Repo: r, CFService: c, Scanner: s, Notifier: n}
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

// [API] 測試 Webhook
func (h *DomainHandler) TestWebhook(c *gin.Context) {
	var req struct {
		WebhookURL string `json:"webhook_url"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要 webhook_url"})
		return
	}

	if err := h.Notifier.SendTestMessage(c.Request.Context(), req.WebhookURL); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "發送失敗: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "測試訊息發送成功"})
}

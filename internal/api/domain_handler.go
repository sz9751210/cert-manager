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

	// [新增] 發送操作通知：同步完成
	// 這裡我們使用 EventAdd 類型，或者您可以定義一個新的 EventSync
	details := fmt.Sprintf("同步來源: Cloudflare, 成功數量: %d", count)
	h.Notifier.NotifyOperation(c.Request.Context(), service.EventAdd, "Cloudflare Sync", details)

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
		ctx := context.Background()

		if err := h.Acme.RenewCertificate(context.Background(), req.Domain); err != nil {
			logrus.Errorf("續簽失敗 %s: %v", req.Domain, err)
			h.Notifier.NotifyOperation(ctx, service.EventRenew, req.Domain, "失敗: "+err.Error())
		} else {
			logrus.Infof("續簽成功 %s", req.Domain)
			h.Notifier.NotifyOperation(ctx, service.EventRenew, req.Domain, "結果: 憑證已更新成功")
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

	if len(objectIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "沒有有效的 ID"})
		return
	}

	if err := h.Repo.BatchUpdateSettings(c.Request.Context(), objectIDs, req.IsIgnored); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// [新增] 發送告警：批量操作
	// 這裡我們可以借用 EventDelete 的模板，或是看作一種更新
	actionType := "批量開啟監控"
	if req.IsIgnored {
		actionType = "批量忽略/停止監控"
	}

	details := fmt.Sprintf("動作: %s, 影響數量: %d 個域名", actionType, len(objectIDs))

	// 這裡暫時用 EventAdd 或自定義的類型，顯示在模板中
	h.Notifier.NotifyOperation(c.Request.Context(), service.EventAdd, "Multiple Domains", details)

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
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment;filename=domains_report.csv")

	// 【關鍵】寫入 UTF-8 BOM，讓 Excel 能正確識別中文
	c.Writer.Write([]byte("\xEF\xBB\xBF"))

	// 3. 寫入 CSV
	writer := csv.NewWriter(c.Writer)
	// 寫入 Header
	writer.Write([]string{"Domain", "Issuer", "Expiry Date", "Days Left", "Status", "Proxy", "Zone"})
	// writer.Write([]string{"域名", "狀態", "剩餘天數", "過期日", "發行商", "主域名", "Proxy開啟"})
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

// [新增] 手動新增域名 API
func (h *DomainHandler) AddDomain(c *gin.Context) {
	var req domain.SSLCertificate
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}

	// 呼叫 Repo 建立
	if err := h.Repo.Create(c.Request.Context(), req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// [新增] 發送告警：新增域名
	details := fmt.Sprintf("來源: 手動新增, IP: %s", c.ClientIP())
	h.Notifier.NotifyOperation(c.Request.Context(), service.EventAdd, req.DomainName, details)

	c.JSON(http.StatusOK, gin.H{"message": "新增成功"})
}

// [新增] 刪除域名 API
func (h *DomainHandler) DeleteDomain(c *gin.Context) {
	id := c.Param("id")
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	// 1. 先查詢要刪除的域名資料 (為了在通知中顯示名稱，不然刪了就不知道是誰了)
	domainCert, err := h.Repo.GetByID(c.Request.Context(), oid)
	domainName := "Unknown"
	if err == nil {
		domainName = domainCert.DomainName
	}

	// 2. 執行刪除
	if err := h.Repo.Delete(c.Request.Context(), oid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 3. [新增] 發送告警：刪除域名
	details := fmt.Sprintf("操作者 IP: %s", c.ClientIP())
	h.Notifier.NotifyOperation(c.Request.Context(), service.EventDelete, domainName, details)

	c.JSON(http.StatusOK, gin.H{"message": "刪除成功"})
}

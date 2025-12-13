package api

import (
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
}

func NewDomainHandler(r repository.DomainRepository, c *service.CloudflareService, s *service.ScannerService) *DomainHandler {
	return &DomainHandler{Repo: r, CFService: c, Scanner: s}
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

	domains, total, err := h.Repo.List(c.Request.Context(), page, limit, sort)
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

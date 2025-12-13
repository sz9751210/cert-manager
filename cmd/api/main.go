package main

import (
	"cert-manager/internal/api"
	"cert-manager/internal/conf"
	"cert-manager/internal/database"
	"cert-manager/internal/repository"
	"cert-manager/internal/service"
	"context"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func main() {
	// 1. Config
	cfg, err := conf.LoadConfig()
	if err != nil {
		logrus.Fatalf("Config error: %v", err)
	}

	// 2. Database
	mongoClient, err := database.Connect(cfg.MongoDB)
	if err != nil {
		logrus.Fatalf("Database error: %v", err)
	}
	defer mongoClient.Disconnect(context.Background())

	db := mongoClient.Database(cfg.MongoDB.Database)

	// 3. Dependency Injection (依賴注入)
	// Repo -> Service -> Handler
	domainRepo := repository.NewMongoDomainRepo(db)
	notifierService := service.NewNotifierService(domainRepo)
	cfService := service.NewCloudflareService(cfg.Cloudflare.APIToken)
	scannerService := service.NewScannerService(domainRepo, notifierService)
	domainHandler := api.NewDomainHandler(domainRepo, cfService, scannerService, notifierService)

	// 4. Gin Router Setup
	r := gin.Default()

	// 設定 CORS (因為前後分離，必須允許跨域)
	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE,PATCH")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	v1 := r.Group("/api/v1")
	{
		v1.POST("/domains/sync", domainHandler.SyncDomains)             // 觸發同步
		v1.POST("/domains/scan", domainHandler.ScanDomains)             // 觸發掃描
		v1.GET("/domains", domainHandler.GetDomains)                    // 列表查詢
		v1.PATCH("/domains/:id/settings", domainHandler.UpdateSettings) // 更新設定
		v1.GET("/zones", domainHandler.GetZones)                        // [新增] 獲取下拉選單資料
		// [新增] 設定路由
		v1.GET("/settings", domainHandler.GetSettings)
		v1.POST("/settings", domainHandler.SaveSettings)
		v1.POST("/settings/test", domainHandler.TestWebhook)
	}

	// 5. Start Server
	logrus.Infof("Server starting on %s", cfg.Server.Port)
	if err := r.Run(cfg.Server.Port); err != nil {
		logrus.Fatalf("Server startup failed: %v", err)
	}
}

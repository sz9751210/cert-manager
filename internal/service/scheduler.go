package service

import (
	"cert-manager/internal/repository"
	"context"

	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
)

type SchedulerService struct {
	Cron    *cron.Cron
	Scanner *ScannerService
	CF      *CloudflareService
	Repo    repository.DomainRepository // 假設您需要直接操作 Repo
}

func NewSchedulerService(scanner *ScannerService, cf *CloudflareService) *SchedulerService {
	// 使用標準 parser (支援 5 個欄位: 分 時 日 月 週)
	c := cron.New()
	return &SchedulerService{
		Cron:    c,
		Scanner: scanner,
		CF:      cf,
	}
}

// Start 啟動排程
func (s *SchedulerService) Start() {
	// 1. 每天凌晨 02:00 自動同步 Cloudflare (確保有新域名進來)
	// Cron 表達式: "0 2 * * *"
	_, err := s.Cron.AddFunc("0 2 * * *", func() {
		logrus.Info("[Cron] 開始自動同步 Cloudflare...")
		// 這裡需要 Context，我們建立一個背景的
		if _, err := s.CF.FetchDomains(context.Background()); err != nil {
			logrus.Errorf("[Cron] Cloudflare 同步失敗: %v", err)
		}
	})
	if err != nil {
		logrus.Error(err)
	}

	// 2. 每天凌晨 02:30 自動掃描 SSL 並告警
	_, err = s.Cron.AddFunc("30 2 * * *", func() {
		logrus.Info("[Cron] 開始自動掃描 SSL...")
		if err := s.Scanner.ScanAll(context.Background()); err != nil {
			logrus.Errorf("[Cron] SSL 掃描失敗: %v", err)
		}
	})
	if err != nil {
		logrus.Error(err)
	}

	s.Cron.Start()
	logrus.Info("自動排程服務已啟動 (每日 02:00 同步, 02:30 掃描)")
}

// Stop 停止排程
func (s *SchedulerService) Stop() {
	s.Cron.Stop()
}

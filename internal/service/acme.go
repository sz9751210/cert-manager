package service

import (
	"cert-manager/internal/domain"
	"cert-manager/internal/repository"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/registration"
	"github.com/sirupsen/logrus"
)

type AcmeService struct {
	Repo    repository.DomainRepository
	CFToken string // 從 Config 讀入
}

func NewAcmeService(repo repository.DomainRepository, cfToken string) *AcmeService {
	return &AcmeService{Repo: repo, CFToken: cfToken}
}

// 取得或創建 ACME 使用者 (如果沒有私鑰就產生新的)
func (s *AcmeService) getUser(ctx context.Context) (*domain.AcmeUser, error) {
	settings, err := s.Repo.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	if settings.AcmeEmail == "" {
		return nil, errors.New("請先在設定中輸入 Email 以啟用 SSL 續簽功能")
	}

	user := &domain.AcmeUser{Email: settings.AcmeEmail}

	// 1. 檢查是否有現存私鑰
	if settings.AcmePrivateKey != "" {
		// 解析私鑰
		key, err := domain.ParsePrivateKey([]byte(settings.AcmePrivateKey))
		if err != nil {
			return nil, fmt.Errorf("私鑰解析失敗: %v", err)
		}
		user.PrivateKey = key

		// 解析註冊資訊
		if settings.AcmeRegData != "" {
			var reg registration.Resource
			json.Unmarshal([]byte(settings.AcmeRegData), &reg)
			user.Registration = &reg
		}
	} else {
		// 2. 沒有私鑰 -> 產生新的 ECDSA 私鑰
		privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		user.PrivateKey = privateKey

		// 儲存私鑰到 DB
		encodedKey, _ := x509.MarshalECPrivateKey(privateKey)
		pemKey := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: encodedKey})

		settings.AcmePrivateKey = string(pemKey)
		s.Repo.SaveSettings(ctx, *settings)
	}

	return user, nil
}

// RenewCertificate 執行續簽 (核心功能)
func (s *AcmeService) RenewCertificate(ctx context.Context, domainName string) error {
	logrus.Infof("開始為 %s 申請憑證...", domainName)

	// 1. 準備使用者
	user, err := s.getUser(ctx)
	if err != nil {
		return err
	}

	// 2. 初始化 Lego Client
	config := lego.NewConfig(user)

	// 注意：為了生產環境安全，這裡使用 Let's Encrypt 正式環境
	// 如果是測試開發，建議先用 lego.LEDirectoryStaging 以免被鎖帳號
	config.CADirURL = lego.LEDirectoryProduction
	config.Certificate.KeyType = certcrypto.EC256

	client, err := lego.NewClient(config)
	if err != nil {
		return err
	}

	// 3. 設定 DNS Provider (Cloudflare)
	cfConfig := cloudflare.NewDefaultConfig()
	cfConfig.AuthToken = s.CFToken

	dnsProvider, err := cloudflare.NewDNSProviderConfig(cfConfig)
	if err != nil {
		return err
	}

	if err := client.Challenge.SetDNS01Provider(dnsProvider); err != nil {
		return err
	}

	// 4. 註冊帳號 (如果還沒註冊)
	if user.Registration == nil {
		logrus.Info("正在向 Let's Encrypt 註冊帳號...")
		reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return fmt.Errorf("ACME 註冊失敗: %v", err)
		}
		user.Registration = reg

		// 更新 DB 裡的註冊資訊
		regData, _ := json.Marshal(reg)
		s.Repo.UpdateAcmeData(ctx, "", "", string(regData))
	}

	// 5. 申請憑證
	request := certificate.ObtainRequest{
		Domains: []string{domainName},
		Bundle:  true,
	}
	certificates, err := client.Certificate.Obtain(request)
	if err != nil {
		return fmt.Errorf("憑證申請失敗: %v", err)
	}

	// 6. 輸出結果 (目前先印出來，下一步我們可以存到 DB 或透過 Webhook 發送)
	logrus.Infof("憑證申請成功！域名: %s", certificates.Domain)
	logrus.Infof("Cert URL: %s", certificates.CertURL)

	// 在真實場景，這裡應該把 certificates.Certificate (公鑰) 和 certificates.PrivateKey (私鑰)
	// 1. 存入資料庫
	// 2. 或是呼叫 Webhook 把憑證內容 POST 到目標伺服器
	// 3. 或是寫入本地檔案系統

	return nil
}

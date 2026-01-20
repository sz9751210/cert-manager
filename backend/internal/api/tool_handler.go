package api

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type ToolHandler struct{}

func NewToolHandler() *ToolHandler {
	return &ToolHandler{}
}

// 定義回傳結構
type CertInfo struct {
	Subject       string    `json:"subject"`
	Issuer        string    `json:"issuer"`
	NotBefore     time.Time `json:"not_before"`
	NotAfter      time.Time `json:"not_after"`
	DaysRemaining int       `json:"days_remaining"`
	DNSNames      []string  `json:"dns_names"` // SANs
	SerialNumber  string    `json:"serial_number"`
	SignatureAlgo string    `json:"signature_algo"`
	IsCA          bool      `json:"is_ca"`
}

// DecodeCertificate 解析使用者上傳的憑證文字
func (h *ToolHandler) DecodeCertificate(c *gin.Context) {
	var req struct {
		CertContent string `json:"cert_content"` // 使用者貼上的 PEM 文字
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "無效的請求格式"})
		return
	}

	// 1. 清理輸入 (去除前後空白)
	certPEM := []byte(strings.TrimSpace(req.CertContent))

	// 2. 解析 PEM Block
	block, _ := pem.Decode(certPEM)
	if block == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "無法解析憑證內容，請確認格式為 PEM (以 -----BEGIN CERTIFICATE----- 開頭)"})
		return
	}

	// 3. 解析 X.509 憑證
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "憑證格式錯誤: " + err.Error()})
		return
	}

	// 4. 提取資訊
	// 處理 Subject (CommonName)
	subject := cert.Subject.CommonName
	if subject == "" && len(cert.Subject.Organization) > 0 {
		subject = cert.Subject.Organization[0]
	}

	// 處理 Issuer
	issuer := cert.Issuer.CommonName
	if issuer == "" && len(cert.Issuer.Organization) > 0 {
		issuer = cert.Issuer.Organization[0]
	}

	// 格式化序號 (BigInt 轉 Hex String)
	serialNumber := fmt.Sprintf("%X", cert.SerialNumber)
    // 加上冒號分隔 (可選，為了美觀)
    serialNumber = formatSerial(serialNumber)

	info := CertInfo{
		Subject:       subject,
		Issuer:        issuer,
		NotBefore:     cert.NotBefore,
		NotAfter:      cert.NotAfter,
		DaysRemaining: int(time.Until(cert.NotAfter).Hours() / 24),
		DNSNames:      cert.DNSNames,
		SerialNumber:  serialNumber,
		SignatureAlgo: cert.SignatureAlgorithm.String(),
		IsCA:          cert.IsCA,
	}

	c.JSON(http.StatusOK, gin.H{"data": info})
}

// 輔助函式：將序號格式化為 AA:BB:CC...
func formatSerial(s string) string {
    var b strings.Builder
    for i, r := range s {
        if i > 0 && i%2 == 0 {
            b.WriteRune(':')
        }
        b.WriteRune(r)
    }
    return b.String()
}
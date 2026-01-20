package domain

import (
	"crypto"
	"crypto/x509"
	"encoding/pem"

	"github.com/go-acme/lego/v4/registration"
)

// AcmeUser 實作 lego 的 User 介面
type AcmeUser struct {
	Email        string
	Registration *registration.Resource
	PrivateKey   crypto.PrivateKey
}

func (u *AcmeUser) GetEmail() string {
	return u.Email
}
func (u *AcmeUser) GetRegistration() *registration.Resource {
	return u.Registration
}
func (u *AcmeUser) GetPrivateKey() crypto.PrivateKey {
	return u.PrivateKey
}

// 輔助函式：從 PEM 字串解析私鑰 (之後會用到)
func ParsePrivateKey(pemBytes []byte) (crypto.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	return x509.ParseECPrivateKey(block.Bytes)
}

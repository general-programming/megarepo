package main

import (
	"crypto/tls"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/hertz/pkg/app/client"
	"github.com/general-programming/gocommon"
	ct "github.com/google/certificate-transparency-go"
	"go.uber.org/zap"
)

func main() {
	ctx := gocommon.CreateContext()
	log := gocommon.CreateLogger()

	clientCfg := &tls.Config{
		InsecureSkipVerify: true,
	}
	c, err := client.NewClient(client.WithTLSConfig(clientCfg))
	if err != nil {
		log.Fatal("create client failed", zap.Error(err))
		return
	}

	status, body, err := c.Get(ctx, nil, "https://ct.googleapis.com/daedalus/ct/v1/get-entries?start=0&end=31")
	if err != nil {
		log.Fatal("get got err", zap.Error(err))
		return
	}

	if status != 200 {
		log.Fatal("get status bad", zap.Int("status", status))
		return
	}

	var resp = &ct.GetEntriesResponse{}

	err = sonic.Unmarshal(body, resp)
	if err != nil {
		log.Fatal("unmarshal failed", zap.Error(err))
		return
	}

	for i, entry := range resp.Entries {
		logEntry, err := ct.LogEntryFromLeaf(int64(i), &entry)
		if err != nil {
			log.Fatal("parse entry failed", zap.Error(err))
			return
		}
		log.Info("cert",
			zap.Any("cn", logEntry.X509Cert.Subject.CommonName),
			zap.Any("issuer", logEntry.X509Cert.Issuer.CommonName),
			zap.Any("notBefore", logEntry.X509Cert.NotBefore),
			zap.Any("notAfter", logEntry.X509Cert.NotAfter),
		)
	}
}

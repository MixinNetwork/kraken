package engine

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"time"
)

type NTS struct {
	URLs             string `json:"urls"`
	Credential       string `json:"credential"`
	CredentialBase64 string `json:"credential_base64"`
	Username         string `json:"username"`
}

func turn(conf *Configuration, uid string) ([]*NTS, error) {
	timestamp := time.Now().Add(1 * time.Hour).Unix()
	username := fmt.Sprintf("%d:%s", timestamp, uid)
	mac := hmac.New(sha1.New, []byte(conf.Turn.Secret))
	if _, err := mac.Write([]byte(username)); err != nil {
		return nil, err
	}
	credentialBuf := mac.Sum(nil)
	credential := base64.StdEncoding.EncodeToString(credentialBuf)
	credentialBase64 := base64.RawURLEncoding.EncodeToString(credentialBuf)
	url := conf.Turn.Host
	ownUDP := &NTS{
		URLs:             url + "?transport=udp",
		Username:         username,
		Credential:       credential,
		CredentialBase64: credentialBase64,
	}
	ownTCP := &NTS{
		URLs:             url + "?transport=tcp",
		Username:         username,
		Credential:       credential,
		CredentialBase64: credentialBase64,
	}
	return []*NTS{ownUDP, ownTCP}, nil
}

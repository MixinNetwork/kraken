package engine

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"time"
)

type NTS struct {
	URL        string `json:"url"`
	Credential string `json:"credential"`
	Username   string `json:"username"`
}

func turn(conf *Configuration, uid string) ([]*NTS, error) {
	timestamp := time.Now().Add(1 * time.Hour).Unix()
	username := fmt.Sprintf("%d:%s", timestamp, uid)
	mac := hmac.New(sha1.New, []byte(conf.Turn.Secret))
	if _, err := mac.Write([]byte(username)); err != nil {
		return nil, err
	}
	credential := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	url := conf.Turn.Host
	ownUDP := &NTS{
		URL:        url + "?transport=udp",
		Username:   username,
		Credential: credential,
	}
	ownTCP := &NTS{
		URL:        url + "?transport=tcp",
		Username:   username,
		Credential: credential,
	}
	return []*NTS{ownUDP, ownTCP}, nil
}

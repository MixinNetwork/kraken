package engine

import (
	"encoding/json"
	"net/http"
)

const (
	ErrorInvalidParams    = 5001000
	ErrorInvalidSDP       = 5001001
	ErrorInvalidCandidate = 5001002
	ErrorRoomFull         = 5002000
	ErrorPeerNotFound     = 5002001
	ErrorPeerClosed       = 5002002
	ErrorTrackNotFound    = 5002003
)

type Error struct {
	Status      int    `json:"status"`
	Code        int    `json:"code"`
	Description string `json:"description"`
}

func (e Error) Error() string {
	b, _ := json.Marshal(e)
	return string(b)
}

func buildError(code int, err error) error {
	return Error{
		Status:      http.StatusAccepted,
		Code:        code,
		Description: err.Error(),
	}
}

package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func assertErrorCode(t *testing.T, err error, code int) {
	t.Helper()
	var rpcError Error
	if !errors.As(err, &rpcError) || rpcError.Code != code {
		t.Errorf("got error %v, want code %d", err, code)
	}
}

func testDescription(kind string) string {
	sdp := "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=rtpmap:111 opus/48000/2\r\n"
	b, _ := json.Marshal(map[string]string{"type": kind, "sdp": sdp})
	return string(b)
}

func TestPublishOptionalParameters(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra []any
		code  int
	}{
		{"three arguments", nil, ErrorInvalidSDP},
		{"five arguments", []any{1, "https://example.invalid/callback"}, ErrorInvalidSDP},
		{"six arguments", []any{1, "https://example.invalid/callback", true}, ErrorInvalidSDP},
		{"invalid limit", []any{"invalid", "https://example.invalid/callback", true}, ErrorInvalidParams},
		{"invalid callback type", []any{1, 123, true}, ErrorInvalidParams},
		{"invalid callback URL", []any{1, "http://example.invalid/callback", true}, ErrorInvalidParams},
		{"invalid listenOnly", []any{1, "https://example.invalid/callback", "invalid"}, ErrorInvalidParams},
		{"four arguments", []any{1}, ErrorInvalidParams},
		{"seven arguments", []any{1, "https://example.invalid/callback", true, "extra"}, ErrorInvalidParams},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := append([]any{"room", "user", "{}"}, tc.extra...)
			_, _, err := (&R{}).publish(params)
			assertErrorCode(t, err, tc.code)
		})
	}
}

func TestPublishPreservesLimitWithListenOnly(t *testing.T) {
	engine := &Engine{rooms: rmapAllocate()}
	_, err := engine.addPeer(&Peer{rid: "room", uid: "existing", cid: "track"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	rpc := &R{router: NewRouter(engine)}
	params := []any{"room", "new-user", testDescription("offer"), 1, "https://example.invalid/callback"}
	_, _, err = rpc.publish(params)
	assertErrorCode(t, err, ErrorRoomFull)
	_, _, err = rpc.publish(append(params, true))
	assertErrorCode(t, err, ErrorRoomFull)
}

func TestRenderErrorPreservesDetails(t *testing.T) {
	structured := Error{Status: http.StatusAccepted, Code: ErrorInvalidParams, Description: "bad parameters"}
	for _, tc := range []struct {
		name string
		err  error
		want Error
	}{
		{"plain", errors.New("transport failed"), Error{Status: http.StatusInternalServerError, Code: ErrorServerInternal, Description: "transport failed"}},
		{"structured", structured, structured},
		{"wrapped", fmt.Errorf("request: %w", structured), structured},
		{"pointer", &structured, structured},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			NewRender(w, &Call{Id: "id", Method: "test"}).RenderError(tc.err)
			var body struct {
				ID    string `json:"id"`
				Error Error  `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.ID != "id" || body.Error != tc.want {
				t.Fatalf("unexpected response: %s", w.Body.String())
			}
		})
	}
}

func TestUnknownMethodHasUsefulError(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"id":"1","method":"typo","params":[]}`))
	(&R{}).handle(w, req, nil)
	var body struct {
		Error Error `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Description != "invalid method typo" || body.Error.Code != ErrorInvalidParams {
		t.Fatalf("error details were lost: %s", w.Body.String())
	}
}

package engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/MixinNetwork/mixin/logger"
	"github.com/dimfeld/httptreemux/v5"
	"github.com/gofrs/uuid/v5"
	"github.com/gorilla/handlers"
	"github.com/pion/webrtc/v4"
	"github.com/unrolled/render"
)

type R struct {
	router *Router
	conf   *Configuration
}

type Call struct {
	Id     string `json:"id"`
	Method string `json:"method"`
	Params []any  `json:"params"`
}

type Render struct {
	w       http.ResponseWriter
	impl    *render.Render
	call    *Call
	startAt time.Time
}

func NewRender(w http.ResponseWriter, c *Call) *Render {
	r := &Render{
		w:       w,
		impl:    render.New(),
		call:    c,
		startAt: time.Now(),
	}
	return r
}

func (r *Render) RenderData(data any) {
	body := map[string]any{"data": data}
	if r.call != nil {
		body["id"] = r.call.Id
	}
	rerr := r.impl.JSON(r.w, http.StatusOK, body)
	if rerr != nil {
		panic(rerr)
	}
	logger.Printf("RPC.handle(id: %s, method: %s, time: %f) OK\n",
		r.call.Id, r.call.Method, time.Since(r.startAt).Seconds())
}

func (r *Render) RenderError(err error) {
	body := map[string]any{"error": errorResponse(err)}
	if r.call != nil {
		body["id"] = r.call.Id
	}
	rerr := r.impl.JSON(r.w, http.StatusOK, body)
	if rerr != nil {
		panic(err)
	}
	logger.Printf("RPC.handle(id: %s, method: %s, time: %f) ERROR %s\n",
		r.call.Id, r.call.Method, time.Since(r.startAt).Seconds(), err.Error())
}

func (impl *R) root(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	info := impl.info()
	renderer := NewRender(w, &Call{Id: uuid.Must(uuid.NewV4()).String(), Method: "root"})
	renderer.RenderData(info)
}

func (impl *R) handle(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	var call Call
	d := json.NewDecoder(r.Body)
	d.UseNumber()
	if err := d.Decode(&call); err != nil {
		renderJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	renderer := NewRender(w, &call)
	logger.Printf("RPC.handle(id: %s, method: %s, params: %v)\n", call.Id, call.Method, call.Params)
	switch call.Method {
	case "turn":
		servers, err := impl.turn(call.Params)
		if err != nil {
			renderer.RenderError(err)
		} else {
			renderer.RenderData(servers)
		}
	case "info":
		info := impl.info()
		renderer.RenderData(info)
	case "list":
		peers, err := impl.list(call.Params)
		if err != nil {
			renderer.RenderError(err)
		} else {
			renderer.RenderData(map[string]any{"peers": peers})
		}
	case "mute":
		peer, err := impl.mute(call.Params)
		if err != nil {
			renderer.RenderError(err)
		} else {
			renderer.RenderData(map[string]any{"peer": peer})
		}
	case "publish":
		cid, answer, err := impl.publish(call.Params)
		if err != nil {
			renderer.RenderError(err)
		} else {
			jsep, _ := json.Marshal(answer)
			renderer.RenderData(map[string]any{"track": cid, "sdp": answer, "jsep": string(jsep)})
		}
	case "restart":
		answer, err := impl.restart(call.Params)
		if err != nil {
			renderer.RenderError(err)
		} else {
			jsep, _ := json.Marshal(answer)
			renderer.RenderData(map[string]any{"jsep": string(jsep)})
		}
	case "end":
		err := impl.end(call.Params)
		if err != nil {
			renderer.RenderError(err)
		} else {
			renderer.RenderData(map[string]string{})
		}
	case "trickle":
		err := impl.trickle(call.Params)
		if err != nil {
			renderer.RenderError(err)
		} else {
			renderer.RenderData(map[string]string{})
		}
	case "subscribe":
		offer, err := impl.subscribe(call.Params)
		if err != nil {
			renderer.RenderError(err)
		} else {
			jsep, _ := json.Marshal(offer)
			renderer.RenderData(map[string]any{"type": offer.Type, "sdp": offer.SDP, "jsep": string(jsep)})
		}
	case "answer":
		err := impl.answer(call.Params)
		if err != nil {
			renderer.RenderError(err)
		} else {
			renderer.RenderData(map[string]string{})
		}
	default:
		renderer.RenderError(buildError(ErrorInvalidParams, fmt.Errorf("invalid method %s", call.Method)))
	}
}

func (r *R) turn(params []any) (any, error) {
	if len(params) != 1 {
		return nil, buildError(ErrorInvalidParams, fmt.Errorf("invalid params count %d", len(params)))
	}
	uid, ok := params[0].(string)
	if !ok {
		return nil, buildError(ErrorInvalidParams, fmt.Errorf("invalid uid type %s", params[0]))
	}
	return turn(r.conf, uid)
}

func (r *R) info() any {
	return r.router.info()
}

func (r *R) list(params []any) ([]map[string]any, error) {
	if len(params) != 1 {
		return nil, buildError(ErrorInvalidParams, fmt.Errorf("invalid params count %d", len(params)))
	}
	rid, ok := params[0].(string)
	if !ok {
		return nil, buildError(ErrorInvalidParams, fmt.Errorf("invalid rid type %s", params[0]))
	}
	return r.router.list(rid)
}

func (r *R) mute(params []any) (map[string]any, error) {
	if len(params) != 2 {
		return nil, buildError(ErrorInvalidParams, fmt.Errorf("invalid params count %d", len(params)))
	}
	rid, ok := params[0].(string)
	if !ok {
		return nil, buildError(ErrorInvalidParams, fmt.Errorf("invalid rid type %s", params[0]))
	}
	uid, ok := params[1].(string)
	if !ok {
		return nil, buildError(ErrorInvalidParams, fmt.Errorf("invalid uid type %s", params[1]))
	}
	peer := r.router.mute(rid, uid)
	if peer == nil {
		return nil, buildError(http.StatusNotFound, fmt.Errorf("peer not found %s", params[1]))
	}
	return peer, nil
}

func (r *R) publish(params []any) (string, *webrtc.SessionDescription, error) {
	if len(params) != 3 && len(params) != 5 && len(params) != 6 {
		return "", nil, buildError(ErrorInvalidParams, fmt.Errorf("invalid params count %d", len(params)))
	}
	rid, ok := params[0].(string)
	if !ok {
		return "", nil, buildError(ErrorInvalidParams, fmt.Errorf("invalid rid type %v", params[0]))
	}
	uid, ok := params[1].(string)
	if !ok {
		return "", nil, buildError(ErrorInvalidParams, fmt.Errorf("invalid uid type %v", params[1]))
	}
	sdp, ok := params[2].(string)
	if !ok {
		return "", nil, buildError(ErrorInvalidParams, fmt.Errorf("invalid sdp type %v", params[2]))
	}
	var limit int
	var callback string
	if len(params) >= 5 {
		i, err := strconv.ParseInt(fmt.Sprint(params[3]), 10, 32)
		if err != nil {
			return "", nil, buildError(ErrorInvalidParams, fmt.Errorf("invalid limit type %v %v", params[3], err))
		}
		limit = int(i)
		cbk, ok := params[4].(string)
		if !ok {
			return "", nil, buildError(ErrorInvalidParams, fmt.Errorf("invalid callback type %v", params[4]))
		}
		if !strings.HasPrefix(cbk, "https://") {
			return "", nil, buildError(ErrorInvalidParams, fmt.Errorf("invalid callback value %s", cbk))
		}
		callback = cbk
	}
	var listenOnly bool
	if len(params) == 6 {
		var err error
		listenOnly, err = strconv.ParseBool(fmt.Sprint(params[5]))
		if err != nil {
			return "", nil, buildError(ErrorInvalidParams, fmt.Errorf("invalid listenOnly value %v: %w", params[5], err))
		}
	}
	return r.router.publish(rid, uid, sdp, limit, callback, listenOnly)
}

func (r *R) restart(params []any) (*webrtc.SessionDescription, error) {
	if len(params) != 4 {
		return nil, buildError(ErrorInvalidParams, fmt.Errorf("invalid params count %d", len(params)))
	}
	ids, err := r.parseId(params)
	if err != nil {
		return nil, buildError(ErrorInvalidParams, err)
	}
	jsep, ok := params[3].(string)
	if !ok {
		return nil, buildError(ErrorInvalidParams, fmt.Errorf("invalid jsep type %s", params[3]))
	}
	return r.router.restart(ids[0], ids[1], ids[2], jsep)
}

func (r *R) end(params []any) error {
	if len(params) != 3 {
		return buildError(ErrorInvalidParams, fmt.Errorf("invalid params count %d", len(params)))
	}
	ids, err := r.parseId(params)
	if err != nil {
		return buildError(ErrorInvalidParams, err)
	}
	return r.router.end(ids[0], ids[1], ids[2])
}

func (r *R) trickle(params []any) error {
	if len(params) != 4 {
		return buildError(ErrorInvalidParams, fmt.Errorf("invalid params count %d", len(params)))
	}
	ids, err := r.parseId(params)
	if err != nil {
		return buildError(ErrorInvalidParams, err)
	}
	candi, ok := params[3].(string)
	if !ok {
		return buildError(ErrorInvalidParams, fmt.Errorf("invalid candi type %s", params[3]))
	}
	return r.router.trickle(ids[0], ids[1], ids[2], candi)
}

func (r *R) subscribe(params []any) (*webrtc.SessionDescription, error) {
	if len(params) != 3 {
		return nil, buildError(ErrorInvalidParams, fmt.Errorf("invalid params count %d", len(params)))
	}
	ids, err := r.parseId(params)
	if err != nil {
		return nil, buildError(ErrorInvalidParams, err)
	}
	return r.router.subscribe(ids[0], ids[1], ids[2])
}

func (r *R) answer(params []any) error {
	if len(params) != 4 {
		return buildError(ErrorInvalidParams, fmt.Errorf("invalid params count %d", len(params)))
	}
	ids, err := r.parseId(params)
	if err != nil {
		return buildError(ErrorInvalidParams, err)
	}
	sdp, ok := params[3].(string)
	if !ok {
		return buildError(ErrorInvalidParams, fmt.Errorf("invalid sdp type %s", params[3]))
	}
	return r.router.answer(ids[0], ids[1], ids[2], sdp)
}

func (r *R) parseId(params []any) ([]string, error) {
	rid, ok := params[0].(string)
	if !ok {
		return nil, fmt.Errorf("invalid rid type %s", params[0])
	}
	uid, ok := params[1].(string)
	if !ok {
		return nil, fmt.Errorf("invalid uid type %s", params[1])
	}
	cid, ok := params[2].(string)
	if !ok {
		return nil, fmt.Errorf("invalid cid type %s", params[2])
	}
	return []string{rid, uid, cid}, nil
}

func registerHandlers(router *httptreemux.TreeMux) {
	router.MethodNotAllowedHandler = func(w http.ResponseWriter, r *http.Request, _ map[string]httptreemux.HandlerFunc) {
		renderJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
	}
	router.NotFoundHandler = func(w http.ResponseWriter, r *http.Request) {
		renderJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
	}
	router.PanicHandler = func(w http.ResponseWriter, r *http.Request, rcv any) {
		logger.Println(rcv)
		renderJSON(w, http.StatusInternalServerError, map[string]any{"error": "server error"})
	}
}

func handleCORS(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			handler.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Add("Access-Control-Allow-Headers", "Content-Type,Authorization,Mixin-Conversation-ID")
		w.Header().Set("Access-Control-Allow-Methods", "OPTIONS,GET,POST,DELETE")
		w.Header().Set("Access-Control-Max-Age", "600")
		if r.Method == "OPTIONS" {
			renderJSON(w, http.StatusOK, map[string]any{})
		} else {
			handler.ServeHTTP(w, r)
		}
	})
}

func renderJSON(w http.ResponseWriter, status int, data any) {
	err := render.New().JSON(w, status, data)
	if err != nil {
		panic(err)
	}
}

func ServeRPC(engine *Engine, conf *Configuration) error {
	logger.Printf("ServeRPC(:%d)\n", conf.RPC.Port)
	impl := &R{router: NewRouter(engine), conf: conf}
	router := httptreemux.New()
	router.GET("/", impl.root)
	router.POST("/", impl.handle)
	registerHandlers(router)
	handler := handleCORS(router)
	handler = handlers.ProxyHeaders(handler)

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", conf.RPC.Port),
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	return server.ListenAndServe()
}

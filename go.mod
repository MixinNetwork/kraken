module github.com/MixinNetwork/kraken

go 1.16

replace github.com/vmihailenco/msgpack/v4 => github.com/MixinNetwork/msgpack/v4 v4.3.13

require (
	github.com/MixinNetwork/mixin v0.13.9
	github.com/dimfeld/httptreemux v5.0.1+incompatible
	github.com/gofrs/uuid v4.1.0+incompatible
	github.com/gorilla/handlers v1.5.1
	github.com/pelletier/go-toml v1.9.4
	github.com/pion/interceptor v0.1.0
	github.com/pion/rtp v1.7.4
	github.com/pion/sdp/v2 v2.4.0
	github.com/pion/webrtc/v3 v3.1.9
	github.com/unrolled/render v1.4.0
)

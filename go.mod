module github.com/MixinNetwork/kraken

go 1.16

replace (
	github.com/pion/webrtc/v3 => github.com/MixinNetwork/pion/v3 v3.0.0-20210223035929-e46acca75b42
	github.com/vmihailenco/msgpack/v4 => github.com/MixinNetwork/msgpack/v4 v4.3.13
)

require (
	github.com/MixinNetwork/mixin v0.11.1
	github.com/dimfeld/httptreemux v5.0.1+incompatible
	github.com/gofrs/uuid v4.0.0+incompatible
	github.com/gorilla/handlers v1.5.1
	github.com/pelletier/go-toml v1.8.1
	github.com/pion/interceptor v0.0.10
	github.com/pion/rtp v1.6.2
	github.com/pion/sdp/v2 v2.4.0
	github.com/pion/webrtc/v3 v3.0.11
	github.com/unrolled/render v1.0.3
)

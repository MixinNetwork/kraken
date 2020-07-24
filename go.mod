module github.com/MixinNetwork/kraken

go 1.14

replace github.com/pion/webrtc/v3 => github.com/MixinNetwork/webrtc/v3 v3.0.0-20200724043408-0bafa1170d88

require (
	github.com/MixinNetwork/mixin v0.8.4
	github.com/dimfeld/httptreemux v5.0.1+incompatible
	github.com/gofrs/uuid v3.3.0+incompatible
	github.com/gorilla/handlers v1.4.2
	github.com/pelletier/go-toml v1.8.0
	github.com/pion/rtcp v1.2.3
	github.com/pion/rtp v1.6.0
	github.com/pion/sdp/v2 v2.4.0
	github.com/pion/webrtc/v3 v3.0.0-20200721060053-ca3cc9d940bc
	github.com/unrolled/render v1.0.3
)

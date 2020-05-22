module github.com/MixinNetwork/kraken

go 1.14

replace github.com/pion/webrtc/v2 => ../../GOPATH/src/github.com/pion/webrtc

//replace github.com/pion/webrtc/v2 => github.com/jeremija/webrtc/v2 v2.2.6-0.20200420091005-4cc16a2df9e0

require (
	github.com/MixinNetwork/mixin v0.7.29
	github.com/dimfeld/httptreemux v5.0.1+incompatible
	github.com/gofrs/uuid v3.3.0+incompatible
	github.com/gorilla/handlers v1.4.2
	github.com/pelletier/go-toml v1.7.0
	github.com/pion/rtcp v1.2.1
	github.com/pion/rtp v1.5.4
	github.com/pion/sdp/v2 v2.3.7
	github.com/pion/webrtc/v2 v2.2.9
	github.com/unrolled/render v1.0.3
)

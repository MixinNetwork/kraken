module github.com/MixinNetwork/kraken

go 1.16

replace github.com/vmihailenco/msgpack/v4 => github.com/MixinNetwork/msgpack/v4 v4.3.13

require (
	github.com/MixinNetwork/mixin v0.12.13
	github.com/dimfeld/httptreemux v5.0.1+incompatible
	github.com/gofrs/uuid v4.0.0+incompatible
	github.com/gorilla/handlers v1.5.1
	github.com/pelletier/go-toml v1.9.0
	github.com/pion/ice/v2 v2.1.12 // indirect
	github.com/pion/interceptor v0.0.15
	github.com/pion/rtp v1.7.2
	github.com/pion/sdp/v2 v2.4.0
	github.com/pion/webrtc/v3 v3.1.0-beta.3
	github.com/unrolled/render v1.1.0
	golang.org/x/crypto v0.0.0-20210817164053-32db794688a5 // indirect
	golang.org/x/net v0.0.0-20210825183410-e898025ed96a // indirect
	golang.org/x/sys v0.0.0-20210823070655-63515b42dcdf // indirect
)

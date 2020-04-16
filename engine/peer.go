package engine

import (
	"fmt"
	"io"
	"sync"

	"github.com/MixinNetwork/mixin/logger"
	"github.com/gofrs/uuid"
	"github.com/pion/webrtc/v2"
)

type Peer struct {
	sync.Mutex
	rid     string
	uid     string
	cid     string
	pc      *webrtc.PeerConnection
	track   *webrtc.Track
	senders map[string]*webrtc.RTPSender
}

func (engine *Engine) AddPeer(rid, uid string, pc *webrtc.PeerConnection) {
	cid, err := uuid.NewV4()
	if err != nil {
		panic(err)
	}
	peer := &Peer{rid: rid, uid: uid, cid: cid.String(), pc: pc}
	peer.senders = make(map[string]*webrtc.RTPSender)
	engine.rooms.Add(peer.rid, peer)
	go engine.HandlePeer(peer)
}

func (p *Peer) id() string {
	return fmt.Sprintf("%s:%s:%s", p.rid, p.uid, p.cid)
}

func (engine *Engine) HandlePeer(peer *Peer) {
	peer.pc.OnSignalingStateChange(func(state webrtc.SignalingState) {
		logger.Printf("HandlePeer(%s) OnSignalingStateChange(%s)\n", peer.id(), state)
	})
	peer.pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		logger.Printf("HandlePeer(%s) OnConnectionStateChange(%s)\n", peer.id(), state)
	})
	peer.pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		logger.Printf("HandlePeer(%s) OnICEConnectionStateChange(%s)\n", peer.id(), state)
	})
	peer.pc.OnTrack(func(rt *webrtc.Track, receiver *webrtc.RTPReceiver) {
		logger.Printf("HandlePeer(%s) OnTrack(%d, %d)\n", peer.id(), rt.PayloadType(), rt.SSRC())
		if webrtc.DefaultPayloadTypeOpus != rt.PayloadType() {
			return
		}

		lt, err := peer.pc.NewTrack(rt.PayloadType(), rt.SSRC(), peer.cid, peer.uid)
		if err != nil {
			panic(err)
		}
		peer.track = lt

		err = copyTrack(rt, lt)
		if err != nil {
			panic(err)
		}
	})
}

func copyTrack(src, dst *webrtc.Track) error {
	buf := make([]byte, 1400)
	for {
		i, err := src.Read(buf)
		if err != nil {
			return err
		}
		// ErrClosedPipe means we don't have any subscribers, this is ok if no peers have connected yet
		_, err = dst.Write(buf[:i])
		if err != nil && err != io.ErrClosedPipe {
			return err
		}
	}
}

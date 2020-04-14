package engine

import (
	"io"

	"github.com/MixinNetwork/mixin/logger"
	"github.com/pion/webrtc/v2"
)

type Peer struct {
	rid   string
	pid   string
	pc    *webrtc.PeerConnection
	track *webrtc.Track
}

func (engine *Engine) AddPeer(rid, pid string, pc *webrtc.PeerConnection) {
	peer := &Peer{rid: rid, pid: pid, pc: pc}
	engine.peersChan <- peer
}

func (engine *Engine) HandlePeer(peer *Peer) {
	peer.pc.OnTrack(func(rt *webrtc.Track, receiver *webrtc.RTPReceiver) {
		if webrtc.DefaultPayloadTypeOpus != rt.PayloadType() {
			logger.Printf("invalid payload %d received from peer %s in room %s\n", rt.PayloadType(), peer.pid, peer.rid)
			return
		}
		lt, err := peer.pc.NewTrack(rt.PayloadType(), rt.SSRC(), peer.pid, peer.rid)
		if err != nil {
			panic(err) // FIXME
		}

		engine.rooms.Iterate(peer.rid, func(p *Peer) {
			if p.pid == peer.pid {
				peer.track = lt
			}
			p.pc.AddTrack(lt)
		})

		rtpBuf := make([]byte, 1400)
		for {
			i, err := rt.Read(rtpBuf)
			if err != nil {
				panic(err)
			}
			// ErrClosedPipe means we don't have any subscribers, this is ok if no peers have connected yet
			_, err = lt.Write(rtpBuf[:i])
			if err != nil && err != io.ErrClosedPipe {
				panic(err)
			}
		}
	})
	engine.rooms.Iterate(peer.rid, func(p *Peer) {
		if p.track != nil && p.pid == peer.pid {
			peer.pc.AddTrack(p.track)
		}
	})
}

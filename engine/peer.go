package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/MixinNetwork/mixin/logger"
	"github.com/gofrs/uuid"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

const (
	peerTrackClosedId          = "CLOSED"
    peerListenOnly             = "LISTEN_ONLY"
	peerTrackConnectionTimeout = 10 * time.Second
	peerTrackReadTimeout       = 30 * time.Second
	rtpBufferSize              = 65536
	rtpClockRate               = 48000
	rtpPacketSequenceMax       = ^uint16(0)
	rtpPacketExpiration        = rtpClockRate / 2
)

var clbkClient *http.Client

func init() {
	clbkClient = &http.Client{
		Timeout: 5 * time.Second,
	}
}

type Sender struct {
	id  string
	rtp *webrtc.RTPSender
}

type NackRequest struct {
	uid  string
	cid  string
	pair *rtcp.NackPair
}

type Peer struct {
	sync.RWMutex
	rid         string
	uid         string
	cid         string
	callback    string
	pc          *webrtc.PeerConnection
	track       *webrtc.Track
	publishers  map[string]*Sender
	subscribers map[string]*Sender
	buffer      []*rtp.Packet
	lost        chan *rtp.Header
	queue       chan *rtp.Packet
	nack        chan *NackRequest
	timestamp   uint32
	sequence    uint16
	connected   chan bool
}

func BuildPeer(rid, uid string, pc *webrtc.PeerConnection, callback string) *Peer {
	cid, err := uuid.NewV4()
	if err != nil {
		panic(err)
	}
	peer := &Peer{rid: rid, uid: uid, cid: cid.String(), pc: pc}
	peer.callback = callback
	peer.connected = make(chan bool, 1)
	peer.lost = make(chan *rtp.Header, 17)
	peer.queue = make(chan *rtp.Packet, 48000)
	peer.nack = make(chan *NackRequest)
	peer.publishers = make(map[string]*Sender)
	peer.subscribers = make(map[string]*Sender)
	peer.buffer = make([]*rtp.Packet, rtpBufferSize)
	peer.handle()
	return peer
}

func (p *Peer) id() string {
	return fmt.Sprintf("%s:%s:%s", p.rid, p.uid, p.cid)
}

func (p *Peer) setPeerCidListenOnly() {
    p.Lock()
	defer p.Unlock()
    p.track = nil
    p.cid = peerListenOnly
}

func (p *Peer) Close() error {
	logger.Printf("PeerClose(%s) now\n", p.id())
	p.Lock()
	defer p.Unlock()

	if p.cid == peerTrackClosedId {
		logger.Printf("PeerClose(%s) already\n", p.id())
		return nil
	}
    
    if p.cid == peerListenOnly {
        logger.Printf("Peer is listen-only, keeping connection open. \n", p.id())
		return nil
    }

	p.track = nil
	p.buffer = nil
	p.cid = peerTrackClosedId
	err := p.pc.Close()
	logger.Printf("PeerClose(%s) with %v\n", p.id(), err)
	return err
}

func (peer *Peer) handle() {
	go func() {
		timer := time.NewTimer(peerTrackConnectionTimeout)
		defer timer.Stop()

		select {
		case <-peer.connected:
		case <-timer.C:
			logger.Printf("HandlePeer(%s) OnTrackTimeout()\n", peer.id())
			peer.Close()
		}
	}()

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
		if peer.track != nil || webrtc.DefaultPayloadTypeOpus != rt.PayloadType() {
			return
		}
		peer.connected <- true

		peer.Lock()
		lt, err := peer.pc.NewTrack(rt.PayloadType(), rt.SSRC(), peer.cid, peer.uid)
		if err != nil {
			panic(err)
		}
		peer.track = lt
		peer.Unlock()

		err = peer.callbackOnTrack()
		if err != nil {
			logger.Printf("HandlePeer(%s) OnTrack(%d, %d) callback error %s\n", peer.id(), rt.PayloadType(), rt.SSRC(), err.Error())
		} else {
			go peer.LoopLost()
			err = peer.copyTrack(rt, lt)
			logger.Printf("HandlePeer(%s) OnTrack(%d, %d) end with %s\n", peer.id(), rt.PayloadType(), rt.SSRC(), err.Error())
		}
		peer.Close()
	})
}

func (peer *Peer) callbackOnTrack() error {
	if peer.callback == "" {
		return nil
	}

	body, _ := json.Marshal(map[string]string{
		"rid":    peer.rid,
		"uid":    peer.uid,
		"cid":    peer.cid,
		"action": "ontrack",
	})
	req, err := http.NewRequest("POST", peer.callback, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := clbkClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("status: %d", resp.StatusCode)
	}
	return nil
}

func (peer *Peer) copyTrack(src, dst *webrtc.Track) error {
	go func() error {
		defer close(peer.queue)

		for {
			pkt, err := src.ReadRTP()
			if err == io.EOF {
				logger.Verbosef("copyTrack(%s) EOF\n", peer.id())
				return nil
			}
			if err != nil {
				logger.Verbosef("copyTrack(%s) error %s\n", peer.id(), err.Error())
				return err
			}
			peer.queue <- pkt
		}
	}()

	defer close(peer.lost)

	timer := time.NewTimer(peerTrackReadTimeout)
	defer timer.Stop()

	for {
		select {
		case r, ok := <-peer.nack:
			if !ok {
				return fmt.Errorf("peer nack closed")
			}
			peer.handleNack(r)
		case pkt, ok := <-peer.queue:
			if !ok {
				return fmt.Errorf("peer queue closed")
			}
			peer.handlePacket(dst, pkt)
		case <-timer.C:
			return fmt.Errorf("peer track read timeout")
		}
		if !timer.Stop() {
			<-timer.C
		}
		timer.Reset(peerTrackReadTimeout)
	}
}
func (peer *Peer) LoopLost() error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	lost := make([]*rtp.Header, 0)
	for peer.track != nil {
		select {
		case p, ok := <-peer.lost:
			if !ok {
				return fmt.Errorf("peer lost closed")
			}
			lost = append(lost, p)
		case <-ticker.C:
		}
		if len(lost) == 0 {
			continue
		}
		fsn := lost[0]
		if len(lost) < 16 && fsn.Timestamp+rtpPacketExpiration/4 > peer.timestamp {
			continue
		}
		blp := uint16(0)
		pair := rtcp.NackPair{PacketID: fsn.SequenceNumber}
		for _, p := range lost {
			if p.SequenceNumber <= pair.PacketID {
				continue
			}
			blp = blp | (1 << (p.SequenceNumber - pair.PacketID - 1))
		}
		pair.LostPackets = rtcp.PacketBitmap(blp)
		pkt := &rtcp.TransportLayerNack{
			SenderSSRC: fsn.SSRC,
			MediaSSRC:  fsn.SSRC,
			Nacks:      []rtcp.NackPair{pair},
		}
		err := peer.pc.WriteRTCP([]rtcp.Packet{pkt})
		logger.Verbosef("LoopLost(%s) %v with %v\n", peer.id(), pair.PacketList(), err)
		if err != nil {
			return err
		}
		lost = make([]*rtp.Header, 0)
	}
	return nil
}

func (peer *Peer) LoopRTCP(uid string, sender *Sender) error {
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()

	queueNack := func(timer *time.Timer, r *NackRequest) error {
		if !timer.Stop() {
			<-timer.C
		}
		timer.Reset(3 * time.Second)

		select {
		case peer.nack <- r:
		case <-timer.C:
			return fmt.Errorf("peer nack queue timeout")
		}
		return nil
	}

	for peer.track != nil {
		pkts, err := sender.rtp.ReadRTCP()
		if err != nil {
			logger.Printf("LoopRTCP(%s,%s,%s) with %v\n", peer.id(), uid, sender.id, err)
			return err
		}
		for _, pkt := range pkts {
			switch pkt.(type) {
			case *rtcp.TransportLayerNack:
				nack := pkt.(*rtcp.TransportLayerNack)
				for _, pair := range nack.Nacks {
					logger.Verbosef("LoopRTCP(%s,%s,%s) TransportLayerNack %v\n", peer.id(), uid, sender.id, pair.PacketList())
					r := &NackRequest{uid: uid, cid: sender.id, pair: &pair}
					err := queueNack(timer, r)
					if err != nil {
						logger.Printf("LoopRTCP(%s,%s,%s) with %v\n", peer.id(), uid, sender.id, err)
						return err
					}
				}
			default:
			}
		}
	}
	return nil
}

func (peer *Peer) handlePacket(dst *webrtc.Track, pkt *rtp.Packet) error {
	peer.RLock()
	buffer := peer.buffer
	peer.RUnlock()
	if buffer == nil {
		return nil
	}

	old := buffer[pkt.SequenceNumber]
	if old != nil && old.Timestamp >= pkt.Timestamp {
		return nil
	}
	if peer.timestamp > pkt.Timestamp+rtpPacketExpiration {
		return nil
	}
	if peer.timestamp == pkt.Timestamp {
		return nil
	}
	if pkt.Timestamp > peer.timestamp {
		peer.handleLost(pkt)
		peer.timestamp = pkt.Timestamp
		peer.sequence = pkt.SequenceNumber
	}
	buffer[pkt.SequenceNumber] = pkt
	return dst.WriteRTP(pkt)
}

func (peer *Peer) handleLost(pkt *rtp.Packet) error {
	gap := pkt.SequenceNumber - peer.sequence
	if pkt.SequenceNumber < peer.sequence {
		gap = rtpPacketSequenceMax - peer.sequence + pkt.SequenceNumber + 1
	}
	if peer.timestamp+rtpPacketExpiration/2 < pkt.Timestamp {
		return nil
	}
	next := (uint32(peer.sequence) + 1) % 65536
	if gap > 17 {
		next = (uint32(peer.sequence) + uint32(gap-17)) % 65536
		gap = 17
	}
	if next+uint32(gap) > 65535 {
		gap = uint16((next + uint32(gap)) % 65536)
		next = 0
	}
	for i := uint16(1); i < gap; i++ {
		peer.lost <- &rtp.Header{
			SequenceNumber: uint16(next),
			Timestamp:      peer.timestamp,
			SSRC:           pkt.SSRC,
		}
		next = next + 1
	}
	return nil
}

func (peer *Peer) handleNack(r *NackRequest) error {
	peer.RLock()
	sender := peer.subscribers[r.uid]
	buffer := peer.buffer
	peer.RUnlock()

	if sender == nil || sender.id != r.cid || buffer == nil {
		return nil
	}

	for _, seq := range r.pair.PacketList() {
		pkt := buffer[seq]
		if pkt == nil {
			continue
		}
		if peer.timestamp > pkt.Timestamp+rtpPacketExpiration {
			continue
		}
		i, err := sender.rtp.SendRTP(&pkt.Header, pkt.Payload)
		logger.Verbosef("HandleNack(%s,%s,%s,%d) with %d %v\n", peer.id(), r.uid, r.cid, seq, i, err)
	}
	return nil
}

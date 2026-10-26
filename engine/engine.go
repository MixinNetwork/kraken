package engine

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/MixinNetwork/mixin/logger"
)

const (
	engineStateLoopPeriod = 60 * time.Second
)

type State struct {
	Version     string    `json:"version"`
	BootedAt    time.Time `json:"booted_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	ActivePeers int       `json:"active_peers"`
	ClosedPeers int       `json:"closed_peers"`
	PeakPeers   int       `json:"peak_peers"`
	ActiveRooms int       `json:"active_rooms"`
	ClosedRooms int       `json:"closed_rooms"`
	PeakRooms   int       `json:"peak_rooms"`
}

type Engine struct {
	IP        string
	Interface string
	PortMin   uint16
	PortMax   uint16

	stateMu   sync.RWMutex
	peakPeers int
	peakRooms int
	state     *State
	rooms     *rmap
	// Retired objects are counted without keeping their connections in memory.
	closedPeers int
	closedRooms int
}

func BuildEngine(conf *Configuration) (*Engine, error) {
	ip, err := getIPFromInterface(conf.Engine.Interface, conf.Engine.Address)
	if err != nil {
		return nil, err
	}
	engine := &Engine{
		IP:        ip,
		Interface: conf.Engine.Interface,
		PortMin:   conf.Engine.PortMin,
		PortMax:   conf.Engine.PortMax,
		rooms:     rmapAllocate(),
	}
	logger.Printf("BuildEngine(IP: %s, Interface: %s, Ports: %d-%d)\n", engine.IP, engine.Interface, engine.PortMin, engine.PortMax)
	return engine, nil
}

func (engine *Engine) Loop(version string) {
	bootedAt := time.Now()

	for {
		engine.updateState(version, bootedAt)
		time.Sleep(engineStateLoopPeriod)
	}
}

func (engine *Engine) updateState(version string, bootedAt time.Time) {
	state := &State{
		Version:   version,
		BootedAt:  bootedAt,
		UpdatedAt: time.Now(),
	}
	// Insertion and cleanup use the same lock order: rooms, room, peer.
	engine.rooms.Lock()
	for rid, room := range engine.rooms.m {
		room.Lock()
		for uid, peer := range room.m {
			if peer.isClosed() {
				delete(room.m, uid)
				engine.closedPeers++
			}
		}
		active := len(room.m)
		room.Unlock()
		if active == 0 {
			delete(engine.rooms.m, rid)
			engine.closedRooms++
		} else {
			state.ActiveRooms++
			state.ActivePeers += active
			logger.Printf("room#%s with %d active peers", rid, active)
		}
	}
	state.ClosedPeers = engine.closedPeers
	state.ClosedRooms = engine.closedRooms
	engine.rooms.Unlock()

	engine.stateMu.Lock()
	defer engine.stateMu.Unlock()
	if state.ActiveRooms > engine.peakRooms {
		engine.peakRooms = state.ActiveRooms
	}
	if state.ActivePeers > engine.peakPeers {
		engine.peakPeers = state.ActivePeers
	}
	state.PeakPeers = engine.peakPeers
	state.PeakRooms = engine.peakRooms
	engine.state = state
}

func getIPFromInterface(iname string, addr string) (string, error) {
	if addr != "" {
		return addr, nil
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, i := range ifaces {
		if i.Name != iname {
			continue
		}
		addrs, err := i.Addrs()
		if err != nil {
			return "", err
		}
		for _, addr := range addrs {
			switch v := addr.(type) {
			case *net.IPNet:
				return v.IP.String(), nil
			case *net.IPAddr:
				return v.IP.String(), nil
			}
		}
	}

	return "", fmt.Errorf("no address for interface %s", iname)
}

type pmap struct {
	sync.RWMutex
	id string
	m  map[string]*Peer
}

func pmapAllocate(id string) *pmap {
	pm := new(pmap)
	pm.id = id
	pm.m = make(map[string]*Peer)
	return pm
}

type rmap struct {
	sync.RWMutex
	m map[string]*pmap
}

func rmapAllocate() *rmap {
	rm := new(rmap)
	rm.m = make(map[string]*pmap)
	return rm
}

func (engine *Engine) getRoom(rid string) *pmap {
	rm := engine.rooms
	rm.RLock()
	defer rm.RUnlock()

	return rm.m[rid]
}

func (engine *Engine) GetRoom(rid string) *pmap {
	pm := engine.getRoom(rid)
	if pm != nil {
		return pm
	}

	rm := engine.rooms
	rm.Lock()
	defer rm.Unlock()
	if rm.m[rid] == nil {
		rm.m[rid] = pmapAllocate(rid)
	}
	return rm.m[rid]
}

func (engine *Engine) getPeer(rid, uid, cid string) (*Peer, error) {
	room := engine.getRoom(rid)
	if room == nil {
		return nil, buildError(ErrorPeerNotFound, fmt.Errorf("peer %s not found in %s", uid, rid))
	}
	return room.GetPeer(uid, cid)
}

// addPeer commits a publication atomically with its capacity check. The caller
// closes a replaced peer after releasing the room locks.
func (engine *Engine) addPeer(peer *Peer, limit int) (*Peer, error) {
	engine.rooms.Lock()
	defer engine.rooms.Unlock()
	room := engine.rooms.m[peer.rid]
	if room == nil {
		room = pmapAllocate(peer.rid)
		engine.rooms.m[peer.rid] = room
	}
	room.Lock()
	defer room.Unlock()
	if err := room.checkLimit(peer.uid, limit); err != nil {
		return nil, err
	}
	old := room.m[peer.uid]
	if old != nil {
		engine.closedPeers++
	}
	room.m[peer.uid] = peer
	return old, nil
}

// checkLimit requires the room lock to be held.
func (room *pmap) checkLimit(uid string, limit int) error {
	if limit <= 0 {
		return nil
	}
	active := 0
	for id, peer := range room.m {
		if id != uid && !peer.isClosed() {
			active++
		}
	}
	if active >= limit {
		return buildError(ErrorRoomFull, fmt.Errorf("room full %d", limit))
	}
	return nil
}

func (room *pmap) PeersCopy() map[string]*Peer {
	room.RLock()
	defer room.RUnlock()

	peers := make(map[string]*Peer, len(room.m))
	for k, v := range room.m {
		peers[k] = v
	}
	return peers
}

func (room *pmap) GetPeer(uid, cid string) (*Peer, error) {
	room.RLock()
	defer room.RUnlock()

	return room.getPeer(uid, cid)
}

func (room *pmap) getPeer(uid, cid string) (*Peer, error) {
	peer := room.m[uid]
	if peer == nil {
		return nil, buildError(ErrorPeerNotFound, fmt.Errorf("peer %s not found in %s", uid, room.id))
	}
	peer.RLock()
	defer peer.RUnlock()
	if peer.closed {
		return nil, buildError(ErrorPeerClosed, fmt.Errorf("peer %s closed in %s", uid, room.id))
	}
	if peer.cid != cid {
		return nil, buildError(ErrorTrackNotFound, fmt.Errorf("peer %s track not match %s %s in %s", uid, cid, peer.cid, room.id))
	}
	return peer, nil
}

package engine

import (
	"fmt"
	"net"
	"sync"

	"github.com/MixinNetwork/mixin/logger"
)

type Engine struct {
	IP        string
	Interface string

	rooms *rmap
}

func BuildEngine(conf *Configuration) (*Engine, error) {
	ip, err := getIPFromInterface(conf.Engine.Interface)
	if err != nil {
		return nil, err
	}
	engine := &Engine{
		IP:        ip,
		Interface: conf.Engine.Interface,
		rooms:     rmapAllocate(),
	}
	logger.Printf("BuildEngine(IP: %s, Interface: %s)\n", engine.IP, engine.Interface)
	return engine, nil
}

func (engine *Engine) Loop() {
}

func getIPFromInterface(in string) (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, i := range ifaces {
		if i.Name != in {
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
	return "", fmt.Errorf("no address for interface %s", in)
}

type pmap struct {
	sync.Mutex
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
	sync.Mutex
	m map[string]*pmap
}

func rmapAllocate() *rmap {
	rm := new(rmap)
	rm.m = make(map[string]*pmap)
	return rm
}

func (engine *Engine) GetRoom(rid string) *pmap {
	rm := engine.rooms
	rm.Lock()
	defer rm.Unlock()
	if rm.m[rid] == nil {
		rm.m[rid] = pmapAllocate(rid)
	}
	return rm.m[rid]
}

func (room *pmap) get(uid, cid string) (*Peer, error) {
	peer := room.m[uid]
	if peer == nil {
		return nil, fmt.Errorf("peer %s not found in %s", uid, room.id)
	}
	if peer.cid != cid {
		return nil, fmt.Errorf("peer %s track not match %s %s in %s", uid, cid, peer.cid, room.id)
	}
	return peer, nil
}

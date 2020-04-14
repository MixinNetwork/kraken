package engine

import (
	"fmt"
	"net"
	"sync"
)

type Engine struct {
	IP        string
	Interface string

	rooms     *rmap
	peersChan chan *Peer
}

func BuildEngine(conf *Configuration) (*Engine, error) {
	ip, err := getIPFromInterface(conf.Engine.Interface)
	if err != nil {
		return nil, err
	}
	engine := &Engine{
		IP:        ip,
		Interface: conf.Engine.Interface,
		rooms:     new(rmap),
		peersChan: make(chan *Peer, conf.Engine.MaxPeerCount),
	}
	return engine, nil
}

func (engine *Engine) Loop() {
	for {
		select {
		case peer := <-engine.peersChan:
			engine.rooms.Add(peer.rid, peer)
			go engine.HandlePeer(peer)
		}
	}
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
			case *net.IPAddr:
				return v.IP.String(), nil
			}
		}
	}
	return "", fmt.Errorf("no address for interface %s", in)
}

type rmap struct {
	sync.Mutex
	m map[string][]*Peer
}

func (rm *rmap) Add(rid string, p *Peer) {
	rm.Lock()
	defer rm.Unlock()

	rm.m[rid] = append(rm.m[rid], p)
}

func (rm *rmap) Iterate(rid string, hook func(*Peer)) {
	rm.Lock()
	defer rm.Unlock()

	for _, p := range rm.m[rid] {
		hook(p)
	}
}

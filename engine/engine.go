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
	m map[string]*Peer
}

func pmapAllocate() *pmap {
	pm := new(pmap)
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

func (rm *rmap) Add(rid string, p *Peer) *Peer {
	rm.Lock()
	if rm.m[rid] == nil {
		rm.m[rid] = pmapAllocate()
	}
	pm := rm.m[rid]
	rm.Unlock()

	pm.Lock()
	defer pm.Unlock()
	old := pm.m[p.uid]
	pm.m[p.uid] = p
	return old
}

func (rm *rmap) Get(rid, uid string) *Peer {
	rm.Lock()
	pm := rm.m[rid]
	rm.Unlock()

	if pm == nil {
		return nil
	}
	pm.Lock()
	defer pm.Unlock()
	return pm.m[uid]
}

func (rm *rmap) Iterate(rid string, hook func(*Peer)) {
	rm.Lock()
	pm := rm.m[rid]
	rm.Unlock()

	pm.Lock()
	defer pm.Unlock()
	for _, p := range pm.m {
		hook(p)
	}
}

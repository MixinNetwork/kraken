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

type rmap struct {
	sync.Mutex
	m map[string]map[string]*Peer
}

func rmapAllocate() *rmap {
	rm := new(rmap)
	rm.m = make(map[string]map[string]*Peer)
	return rm
}

func (rm *rmap) Add(rid string, p *Peer) {
	rm.Lock()
	defer rm.Unlock()

	if rm.m[rid] == nil {
		rm.m[rid] = make(map[string]*Peer)
	}
	rm.m[rid][p.uid] = p
}

func (rm *rmap) Get(rid, uid string) *Peer {
	rm.Lock()
	defer rm.Unlock()

	if rm.m[rid] == nil {
		return nil
	}
	return rm.m[rid][uid]
}

func (rm *rmap) Iterate(rid string, hook func(*Peer)) {
	rm.Lock()
	defer rm.Unlock()

	for _, p := range rm.m[rid] {
		hook(p)
	}
}

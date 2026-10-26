package engine

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRoomLookupsDoNotCreateRooms(t *testing.T) {
	engine := &Engine{rooms: rmapAllocate()}
	router := NewRouter(engine)
	for i := range 100 {
		rid := fmt.Sprint(i)
		peers, err := router.list(rid)
		if err != nil || len(peers) != 0 {
			t.Fatalf("list missing room: %v, %v", peers, err)
		}
		if router.mute(rid, "user") != nil {
			t.Fatal("mute returned a nonexistent peer")
		}
		assertErrorCode(t, router.end(rid, "user", "track"), ErrorPeerNotFound)
		_, err = router.subscribe(rid, "user", "track")
		assertErrorCode(t, err, ErrorPeerNotFound)
		_, err = router.restart(rid, "user", "track", "{}")
		assertErrorCode(t, err, ErrorPeerNotFound)
		err = router.trickle(rid, "user", "track", `{"candidate":"candidate:1 1 udp 1 127.0.0.1 9000 typ host"}`)
		assertErrorCode(t, err, ErrorPeerNotFound)
		err = router.answer(rid, "user", "track", testDescription("answer"))
		assertErrorCode(t, err, ErrorPeerNotFound)
	}
	if n := len(engine.rooms.m); n != 0 {
		t.Fatalf("lookups retained %d nonexistent rooms", n)
	}
}

func TestConcurrentRoomCapacity(t *testing.T) {
	engine := &Engine{rooms: rmapAllocate()}
	const limit = 4
	const attempts = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	var admitted atomic.Int32
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			peer := &Peer{rid: "room", uid: fmt.Sprint(i), cid: fmt.Sprint(i)}
			if _, err := engine.addPeer(peer, limit); err != nil {
				assertErrorCode(t, err, ErrorRoomFull)
			} else {
				admitted.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if n := admitted.Load(); n != limit {
		t.Fatalf("admitted %d peers with a limit of %d", n, limit)
	}
	room := engine.getRoom("room")
	if n := len(room.PeersCopy()); n != limit {
		t.Fatalf("room contains %d peers with a limit of %d", n, limit)
	}
	for uid, old := range room.PeersCopy() {
		replacement := &Peer{rid: "room", uid: uid, cid: "replacement"}
		got, err := engine.addPeer(replacement, limit)
		if err != nil || got != old {
			t.Fatalf("replacement in a full room: old=%p, err=%v", got, err)
		}
		break
	}
}

func TestCleanupRetiresPeersAndRooms(t *testing.T) {
	engine := &Engine{rooms: rmapAllocate()}
	active := testPeer(t, "room", "active")
	closed := testPeer(t, "room", "closed")
	last := testPeer(t, "other-room", "last")
	for _, peer := range []*Peer{active, closed, last} {
		if _, err := engine.addPeer(peer, 0); err != nil {
			t.Fatal(err)
		}
	}
	bootedAt := time.Now()
	engine.updateState("test", bootedAt)
	for _, peer := range []*Peer{closed, last} {
		if err := peer.CloseWithTimeout(); err != nil {
			t.Fatal(err)
		}
	}
	engine.updateState("test", bootedAt)
	state := NewRouter(engine).info().(*State)
	if state.ActivePeers != 1 || state.ActiveRooms != 1 || state.ClosedPeers != 2 || state.ClosedRooms != 1 || state.PeakPeers != 3 || state.PeakRooms != 2 {
		t.Fatalf("unexpected state after cleanup: %+v", state)
	}
	if engine.getRoom("other-room") != nil || len(engine.getRoom("room").PeersCopy()) != 1 {
		t.Fatal("cleanup retained closed peers or an empty room")
	}
	if err := active.CloseWithTimeout(); err != nil {
		t.Fatal(err)
	}
	engine.updateState("test", bootedAt)
	engine.updateState("test", bootedAt)
	state = NewRouter(engine).info().(*State)
	if len(engine.rooms.m) != 0 || state.ClosedPeers != 3 || state.ClosedRooms != 2 {
		t.Fatalf("cleanup did not preserve retirement totals: %+v", state)
	}
}

func TestCleanupConcurrentWithPublishingAndInfo(t *testing.T) {
	engine := &Engine{rooms: rmapAllocate()}
	router := NewRouter(engine)
	bootedAt := time.Now()
	const attempts = 100
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := range attempts {
			peer := &Peer{rid: "room", uid: fmt.Sprint(i), cid: fmt.Sprint(i), closed: i%2 == 0}
			if _, err := engine.addPeer(peer, 0); err != nil {
				t.Error(err)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range attempts {
			engine.updateState("test", bootedAt)
		}
	}()
	go func() {
		defer wg.Done()
		for range attempts {
			_ = router.info()
			_, _ = router.list("room")
		}
	}()
	wg.Wait()
	engine.updateState("test", bootedAt)
	state := router.info().(*State)
	if state.ActivePeers != attempts/2 || state.ClosedPeers != attempts/2 || len(engine.getRoom("room").PeersCopy()) != attempts/2 {
		t.Fatalf("cleanup lost or retained peers during publication: %+v", state)
	}
}

func TestConcurrentMuteAndList(t *testing.T) {
	engine := &Engine{rooms: rmapAllocate()}
	peer := &Peer{rid: "room", uid: "user", cid: "track"}
	if _, err := engine.addPeer(peer, 0); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(engine)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				router.mute("room", "user")
				_, _ = router.list("room")
			}
		}()
	}
	wg.Wait()
	if peer.listenOnly {
		t.Fatal("concurrent mute requests lost an update")
	}
}

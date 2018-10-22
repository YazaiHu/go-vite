package p2p

import (
	"fmt"
	"github.com/vitelabs/go-vite/common"
	"github.com/vitelabs/go-vite/monitor"
	"io"
	"net"
	"sync"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/pkg/errors"
	"github.com/vitelabs/go-vite/log15"
	"github.com/vitelabs/go-vite/p2p/discovery"
	"github.com/vitelabs/go-vite/p2p/protos"
)

const Version uint64 = 2
const baseProtocolCmdSet = 0
const handshakeCmd = 0
const discCmd = 1

const headerLength = 40
const maxPayloadSize = ^uint32(0) >> 8 // 16MB

const paralProtoFrame = 3 // max number of protoFrame write concurrently

var errMsgTooLarge = errors.New("message payload is too large")
var errMsgNull = errors.New("message payload is 0 byte")
var errPeerTermed = errors.New("peer has been terminated")

//var errPeerTsBusy = errors.New("peer transport is busy, can`t write message")

type conn struct {
	*AsyncMsgConn
	flags      connFlag
	cmdSets    []CmdSet
	name       string
	localID    discovery.NodeID
	localIP    net.IP
	localPort  uint16
	remoteID   discovery.NodeID
	remoteIP   net.IP
	remotePort uint16
}

func (c *conn) is(flag connFlag) bool {
	return c.flags.is(flag)
}

type protoFrame struct {
	*Protocol
	*conn
	input    chan *Msg
	term     chan struct{}
	canWrite chan struct{} // if this frame can write message
}

func newProtoFrame(protocol *Protocol, conn *conn) *protoFrame {
	return &protoFrame{
		Protocol: protocol,
		conn:     conn,
		input:    make(chan *Msg, 100),
	}
}

func (pf *protoFrame) ReadMsg() (msg *Msg, err error) {
	select {
	case <-pf.term:
		return msg, io.EOF
	case msg = <-pf.input:
		return
	}
}

func (pf *protoFrame) WriteMsg(msg *Msg) (err error) {
	return pf.conn.SendMsg(msg)
}

// create multiple protoFrames above the rw
func createProtoFrames(ourSet []*Protocol, theirSet []CmdSet, conn *conn) protoFrameMap {
	protoFrames := make(map[uint64]*protoFrame)
	for _, our := range ourSet {
		for _, theirID := range theirSet {
			if our.ID == theirID {
				protoFrames[our.ID] = newProtoFrame(our, conn)
			}
		}
	}

	return protoFrames
}

// event
type protoDone struct {
	id  uint64
	err error
}

// @section Peer
type protoFrameMap = map[uint64]*protoFrame
type Peer struct {
	ts          *conn
	protoFrames protoFrameMap
	lock        sync.RWMutex
	created     time.Time
	wg          sync.WaitGroup
	term        chan struct{}
	disc        chan DiscReason // disconnect proactively
	errch       chan error      // for common error
	protoDone   chan *protoDone // for protocols
	log         log15.Logger
}

func NewPeer(conn *conn, ourSet []*Protocol) (*Peer, error) {
	protoFrames := createProtoFrames(ourSet, conn.cmdSets, conn)

	if len(protoFrames) == 0 {
		return nil, DiscUselessPeer
	}

	p := &Peer{
		ts:          conn,
		protoFrames: protoFrames,
		term:        make(chan struct{}),
		created:     time.Now(),
		disc:        make(chan DiscReason),
		errch:       make(chan error),
		protoDone:   make(chan *protoDone, len(protoFrames)),
		log:         log15.New("module", "p2p/peer"),
	}

	p.ts.handler = p.handleMsg

	return p, nil
}

func (p *Peer) Disconnect(reason DiscReason) {
	p.log.Info("disconnected", "peer", p.String(), "reason", reason)

	select {
	case <-p.term:
	case p.disc <- reason:
	}
}

func (p *Peer) startProtocols() {
	p.wg.Add(len(p.protoFrames))
	canWrite := make(chan struct{}, paralProtoFrame)

	for _, pf := range p.protoFrames {
		// closure
		protoFrame := pf
		common.Go(func() {
			p.runProtocol(protoFrame, canWrite)
		})
	}
}

func (p *Peer) runProtocol(proto *protoFrame, canWrite chan struct{}) {
	defer p.wg.Done()
	proto.term = p.term
	proto.canWrite = canWrite

	err := proto.Handle(p, proto)
	p.protoDone <- &protoDone{proto.ID, err}
}

func (p *Peer) run() (err error) {
	p.log.Info(fmt.Sprintf("peer %s run", p))

	p.ts.Start()

	p.startProtocols()

	var proactively bool // whether we want to disconnect or not
	var reason DiscReason

loop:
	for {
		select {
		case reason = <-p.disc:
			err = reason
			p.log.Error(fmt.Sprintf("disconnected: %v", err))
			proactively = true
			break loop
		case e := <-p.protoDone:
			if pf, ok := p.protoFrames[e.id]; ok {

				p.log.Error(fmt.Sprintf("protocol %s is done: %v", pf.String(), e.err))

				p.lock.Lock()
				delete(p.protoFrames, e.id)
				p.lock.Unlock()

				if err = e.err; err != nil {
					reason = DiscProtocolError
					proactively = true
					break loop
				}

				if len(p.protoFrames) == 0 {
					reason = DiscAllProtocolDone
					err = DiscAllProtocolDone
					proactively = true
					break loop
				}
			}

		case err = <-p.ts.errch:
			// error occur from lower transport, like writeError or readError
			p.log.Error(fmt.Sprintf("transport error: %v", err))
			proactively = false
			break loop
		case err = <-p.errch:
			p.log.Error(fmt.Sprintf("peer error: %v", err))
			proactively = false
			break loop
		}
	}

	close(p.term)

	if proactively {
		if m, e := PackMsg(baseProtocolCmdSet, discCmd, 0, reason); e == nil {
			p.ts.SendMsg(m)
		}
	}
	p.ts.Close()

	p.wg.Wait()

	p.log.Info(fmt.Sprintf("peer %s run done: %v", p, err))
	return err
}

func (p *Peer) handleMsg(msg *Msg) {
	cmdset, cmd := msg.CmdSet, msg.Cmd

	select {
	case <-p.term:
		p.log.Error(fmt.Sprintf("peer has been terminated, can`t handle message %d/%d", cmdset, cmd))
		msg.Recycle()
	default:
		if cmdset == baseProtocolCmdSet {
			switch cmd {
			case discCmd:
				if reason, err := DeserializeDiscReason(msg.Payload); err == nil {
					p.errch <- reason
				} else {
					p.errch <- err
				}
			default:
				msg.Recycle()
			}
		} else {
			var pf *protoFrame
			p.lock.RLock()
			pf = p.protoFrames[cmdset]
			p.lock.RUnlock()

			if pf != nil {
				select {
				case pf.input <- msg:
				default:
					monitor.LogEvent("p2p", "discard")
					p.log.Warn(fmt.Sprintf("protocol is busy, discard message %d/%d", cmdset, cmd))
					msg.Recycle()
				}
			} else {
				p.log.Error(fmt.Sprintf("missing suitable protocol to handle message %d/%d", cmdset, cmd))
				p.disc <- DiscUnKnownProtocol
			}
		}
	}
}

func (p *Peer) ID() discovery.NodeID {
	return p.ts.remoteID
}

func (p *Peer) Name() string {
	return p.ts.name
}

func (p *Peer) String() string {
	return p.ID().String() + "@" + p.RemoteAddr().String()
}

func (p *Peer) RemoteAddr() *net.TCPAddr {
	return p.ts.fd.RemoteAddr().(*net.TCPAddr)
}

func (p *Peer) IP() net.IP {
	return p.RemoteAddr().IP
}

func (p *Peer) Info() *PeerInfo {
	pts := make([]string, len(p.protoFrames))
	i := 0
	for _, pf := range p.protoFrames {
		pts[i] = pf.String()
		i++
	}

	return &PeerInfo{
		ID:        p.ID().String(),
		Name:      p.Name(),
		Protocols: pts,
		Address:   p.RemoteAddr().String(),
		Inbound:   p.ts.is(inbound),
	}
}

// @section PeerSet
type PeerSet struct {
	peers    map[discovery.NodeID]*Peer
	inbound  int
	outbound int
	size     uint
}

func NewPeerSet() *PeerSet {
	return &PeerSet{
		peers: make(map[discovery.NodeID]*Peer),
	}
}

func (s *PeerSet) Add(p *Peer) {
	s.peers[p.ID()] = p
	if p.ts.is(inbound) {
		s.inbound++
	} else {
		s.outbound++
	}

	s.size++
}

func (s *PeerSet) Del(p *Peer) {
	delete(s.peers, p.ID())

	if p.ts.is(inbound) {
		s.inbound--
	} else {
		s.outbound--
	}

	s.size--
}

func (s *PeerSet) Has(id discovery.NodeID) bool {
	_, ok := s.peers[id]
	return ok
}

func (s *PeerSet) Size() uint {
	return s.size
}

func (s *PeerSet) Info() []*PeerInfo {
	info := make([]*PeerInfo, s.Size())
	i := 0
	for _, p := range s.peers {
		info[i] = p.Info()
		i++
	}

	return info
}

func (s *PeerSet) Traverse(fn func(id discovery.NodeID, p *Peer)) {
	for id, p := range s.peers {
		id, p := id, p
		fn(id, p)
	}
}

// @section PeerInfo
type PeerInfo struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Protocols []string `json:"protocols"`
	Address   string   `json:"address"`
	Inbound   bool     `json:"inbound"`
}

// @section ConnProperty
type ConnProperty struct {
	LocalID    string `json:"localID"`
	LocalIP    net.IP `json:"localIP"`
	LocalPort  uint16 `json:"localPort"`
	RemoteID   string `json:"remoteID"`
	RemoteIP   net.IP `json:"remoteIP"`
	RemotePort uint16 `json:"remotePort"`
}

func (cp *ConnProperty) Serialize() ([]byte, error) {
	return proto.Marshal(cp.Proto())
}

func (cp *ConnProperty) Deserialize(buf []byte) error {
	pb := new(protos.ConnProperty)
	err := proto.Unmarshal(buf, pb)
	if err != nil {
		return err
	}

	cp.Deproto(pb)
	return nil
}

func (cp *ConnProperty) Proto() *protos.ConnProperty {
	return &protos.ConnProperty{
		LocalID:    cp.LocalID,
		LocalIP:    cp.LocalIP,
		LocalPort:  uint32(cp.LocalPort),
		RemoteID:   cp.RemoteID,
		RemoteIP:   cp.RemoteIP,
		RemotePort: uint32(cp.RemotePort),
	}
}

func (cp *ConnProperty) Deproto(pb *protos.ConnProperty) {
	cp.LocalID = pb.LocalID
	cp.LocalIP = pb.LocalIP
	cp.LocalPort = uint16(pb.LocalPort)

	cp.RemoteID = pb.RemoteID
	cp.RemoteIP = pb.RemoteIP
	cp.RemotePort = uint16(pb.RemotePort)
}

func (p *Peer) GetConnProperty() *ConnProperty {
	return &ConnProperty{
		LocalID:    p.ts.localID.String(),
		LocalIP:    p.ts.localIP,
		LocalPort:  p.ts.localPort,
		RemoteID:   p.ts.remoteID.String(),
		RemoteIP:   p.ts.remoteIP,
		RemotePort: p.ts.remotePort,
	}
}

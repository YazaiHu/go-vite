package p2p

import (
	"github.com/golang/protobuf/proto"
	"github.com/vitelabs/go-vite/p2p/discovery"
	"github.com/vitelabs/go-vite/p2p/protos"
	"net"
	"strconv"
	"time"
)

// @section NetworkID
type NetworkID uint64

const (
	MainNet NetworkID = iota + 1
	TestNet
	Aquarius
	Pisces
	Aries
	Taurus
	Gemini
	Cancer
	Leo
	Virgo
	Libra
	Scorpio
	Sagittarius
	Capricorn
)

var network = [...]string{
	MainNet:     "MainNet",
	TestNet:     "TestNet",
	Aquarius:    "Aquarius",
	Pisces:      "Pisces",
	Aries:       "Aries",
	Taurus:      "Taurus",
	Gemini:      "Gemini",
	Cancer:      "Cancer",
	Leo:         "Leo",
	Virgo:       "Virgo",
	Libra:       "Libra",
	Scorpio:     "Scorpio",
	Sagittarius: "Sagittarius",
	Capricorn:   "Capricorn",
}

func (i NetworkID) String() string {
	if i >= MainNet && i <= Capricorn {
		return network[i]
	}

	return "Unknown"
}

// @section connFlag
type connFlag int

const (
	outbound connFlag = 1 << iota
	inbound
	static
)

func (f connFlag) is(f2 connFlag) bool {
	return (f & f2) != 0
}

// @section Msg
type CmdSet = uint64
type Msg struct {
	CmdSet     uint64
	Cmd        uint32
	Id         uint64 // as message context
	Size       uint32 // how many bytes in payload, used to quickly determine whether payload is valid
	Payload    []byte
	ReceivedAt time.Time
}

func (msg *Msg) Recycle() {
	//msg.CmdSet = 0
	//msg.Cmd = 0
	//msg.Size = 0
	//msg.Payload = nil
	//msg.Id = 0
	//
	//msgPool.Put(msg)
}

type MsgReader interface {
	ReadMsg() (*Msg, error)
}

type MsgWriter interface {
	WriteMsg(*Msg) error
}

type MsgReadWriter interface {
	MsgReader
	MsgWriter
}

type Serializable interface {
	Serialize() ([]byte, error)
	Deserialize(buf []byte) error
}

// @section protocol
type Protocol struct {
	// description of the protocol
	Name string
	// use for message command set, should be unique
	ID uint64
	// read and write Msg with rw
	Handle func(p *Peer, rw MsgReadWriter) error
}

func (p *Protocol) String() string {
	return p.Name + "/" + strconv.FormatUint(p.ID, 10)
}

// handshake message
type Handshake struct {
	Version uint64
	// peer name, use for readability and log
	Name string
	// running at which network
	NetID NetworkID
	// peer remoteID
	ID discovery.NodeID
	// command set supported
	CmdSets []CmdSet
	// peer`s IP
	RemoteIP net.IP
	// peer`s Port
	RemotePort uint16
}

func (hs *Handshake) Serialize() ([]byte, error) {
	return proto.Marshal(&protos.Handshake{
		NetID:      uint64(hs.NetID),
		Name:       hs.Name,
		ID:         hs.ID[:],
		CmdSets:    hs.CmdSets,
		RemoteIP:   hs.RemoteIP,
		RemotePort: uint32(hs.RemotePort),
	})
}

func (hs *Handshake) Deserialize(buf []byte) error {
	pb := new(protos.Handshake)
	err := proto.Unmarshal(buf, pb)
	if err != nil {
		return err
	}

	id, err := discovery.Bytes2NodeID(pb.ID)
	if err != nil {
		return err
	}

	hs.Version = pb.Version
	hs.ID = id
	hs.NetID = NetworkID(pb.NetID)
	hs.Name = pb.Name
	hs.RemoteIP = pb.RemoteIP
	hs.RemotePort = uint16(pb.RemotePort)
	hs.CmdSets = pb.CmdSets

	return nil
}

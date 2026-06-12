package ipc

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"sync"

	"github.com/vmihailenco/msgpack/v5"

	"github.com/gandr-net/gandr/pkg/proto"
)

// Handler is implemented by the daemon to service client requests.
type Handler interface {
	// HandleSend routes a complete signed envelope from the client.
	HandleSend(env *proto.Envelope) error
	// HandleFetch retrieves an envelope by content hash.
	HandleFetch(hash [32]byte) (*proto.Envelope, error)
	// HandlePeerList reports current peers.
	HandlePeerList() []PeerInfo
	// HandleProfile returns the latest profile envelope for a pubkey.
	HandleProfile(pubkey [32]byte) (*proto.Envelope, error)
	// HandleTrust sets the local trust level for a peer.
	HandleTrust(identity [32]byte, level uint8) error
	// HandleConnect initiates federation with a yggdrasil node key. It
	// returns once the attempt is queued, not once it completes.
	HandleConnect(yggKey [32]byte) error
}

// Server accepts gandr client connections on a Unix socket.
type Server struct {
	handler  Handler
	listener net.Listener

	mu    sync.Mutex
	conns map[*serverConn]struct{}
	done  bool
}

// serverConn is one connected client.
type serverConn struct {
	conn    net.Conn
	writeMu sync.Mutex

	mu   sync.Mutex
	subs map[[32]byte]struct{} // subscribed channel ids
}

// Listen creates the socket (removing any stale one) and starts
// accepting clients. The socket is mode 0660: the daemon's owner and
// group only. System installs put local users in the daemon's group to
// grant client access — the same pattern as docker.sock.
func Listen(socketPath string, handler Handler) (*Server, error) {
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("ipc: removing stale socket: %w", err)
	}
	l, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("ipc: listening on %s: %w", socketPath, err)
	}
	if err := os.Chmod(socketPath, 0o660); err != nil {
		l.Close()
		return nil, fmt.Errorf("ipc: restricting socket permissions: %w", err)
	}
	s := &Server{
		handler:  handler,
		listener: l,
		conns:    make(map[*serverConn]struct{}),
	}
	go s.acceptLoop()
	return s, nil
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		sc := &serverConn{conn: conn, subs: make(map[[32]byte]struct{})}
		s.mu.Lock()
		if s.done {
			s.mu.Unlock()
			conn.Close()
			return
		}
		s.conns[sc] = struct{}{}
		s.mu.Unlock()
		go s.serve(sc)
	}
}

// serve handles one client until disconnect.
func (s *Server) serve(sc *serverConn) {
	defer func() {
		s.mu.Lock()
		delete(s.conns, sc)
		s.mu.Unlock()
		sc.conn.Close()
	}()
	for {
		f, err := ReadFrame(sc.conn)
		if err != nil {
			return
		}
		s.dispatch(sc, f)
	}
}

func (s *Server) dispatch(sc *serverConn, f Frame) {
	switch f.Type {
	case IPCSend:
		env, err := proto.Decode(f.Payload)
		if err != nil {
			sc.sendError(f.RequestID, "invalid envelope")
			return
		}
		if err := s.handler.HandleSend(env); err != nil {
			sc.sendError(f.RequestID, err.Error())
			return
		}
		sc.reply(Frame{Type: IPCSend, RequestID: f.RequestID})
	case IPCSubscribe, IPCUnsubscribe:
		if len(f.Payload) != 32 {
			sc.sendError(f.RequestID, "invalid channel id")
			return
		}
		var ch [32]byte
		copy(ch[:], f.Payload)
		sc.mu.Lock()
		if f.Type == IPCSubscribe {
			sc.subs[ch] = struct{}{}
		} else {
			delete(sc.subs, ch)
		}
		sc.mu.Unlock()
		sc.reply(Frame{Type: f.Type, RequestID: f.RequestID})
	case IPCFetch:
		if len(f.Payload) != 32 {
			sc.sendError(f.RequestID, "invalid hash")
			return
		}
		var hash [32]byte
		copy(hash[:], f.Payload)
		env, err := s.handler.HandleFetch(hash)
		if err != nil {
			sc.sendError(f.RequestID, "not found")
			return
		}
		sc.reply(Frame{Type: IPCFetch, RequestID: f.RequestID, Payload: env.Encode()})
	case IPCPeerList:
		data, err := msgpack.Marshal(s.handler.HandlePeerList())
		if err != nil {
			sc.sendError(f.RequestID, "internal error")
			return
		}
		sc.reply(Frame{Type: IPCPeerList, RequestID: f.RequestID, Payload: data})
	case IPCProfile:
		if len(f.Payload) != 32 {
			sc.sendError(f.RequestID, "invalid pubkey")
			return
		}
		var pk [32]byte
		copy(pk[:], f.Payload)
		env, err := s.handler.HandleProfile(pk)
		if err != nil {
			sc.sendError(f.RequestID, "not found")
			return
		}
		sc.reply(Frame{Type: IPCProfile, RequestID: f.RequestID, Payload: env.Encode()})
	case IPCTrust:
		if len(f.Payload) != 33 {
			sc.sendError(f.RequestID, "invalid trust request")
			return
		}
		var id [32]byte
		copy(id[:], f.Payload[:32])
		if err := s.handler.HandleTrust(id, f.Payload[32]); err != nil {
			sc.sendError(f.RequestID, err.Error())
			return
		}
		sc.reply(Frame{Type: IPCTrust, RequestID: f.RequestID})
	case IPCConnect:
		if len(f.Payload) != 32 {
			sc.sendError(f.RequestID, "invalid node key")
			return
		}
		var key [32]byte
		copy(key[:], f.Payload)
		if err := s.handler.HandleConnect(key); err != nil {
			sc.sendError(f.RequestID, err.Error())
			return
		}
		sc.reply(Frame{Type: IPCConnect, RequestID: f.RequestID})
	default:
		sc.sendError(f.RequestID, "unknown request type")
	}
}

func (sc *serverConn) reply(f Frame) {
	sc.writeMu.Lock()
	defer sc.writeMu.Unlock()
	WriteFrame(sc.conn, f)
}

func (sc *serverConn) sendError(reqID uint32, msg string) {
	data, err := msgpack.Marshal(&ErrorPayload{Message: msg})
	if err != nil {
		return
	}
	sc.reply(Frame{Type: IPCError, RequestID: reqID, Payload: data})
}

// subscribed reports whether the client wants envelopes for this
// recipient. Direct messages (anything that is not a channel broadcast)
// always pass; the single-user client filters further itself.
func (sc *serverConn) wants(env *proto.Envelope) bool {
	if env.Type != proto.MsgChat {
		return true
	}
	chat := &proto.ChatPayload{}
	if err := proto.DecodePayload(env.Payload, chat); err != nil {
		return false
	}
	if chat.ChannelID == ([32]byte{}) {
		return true // DM
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	_, ok := sc.subs[chat.ChannelID]
	return ok
}

// Push streams an incoming envelope to all connected clients that want
// it.
func (s *Server) Push(env *proto.Envelope) {
	s.broadcast(env, IPCIncoming)
}

// Delivered notifies clients of a delivery confirmation envelope
// (sealed ack or generic ack).
func (s *Server) Delivered(env *proto.Envelope) {
	s.broadcast(env, IPCDelivered)
}

func (s *Server) broadcast(env *proto.Envelope, frameType uint8) {
	data := env.Encode()
	s.mu.Lock()
	conns := make([]*serverConn, 0, len(s.conns))
	for sc := range s.conns {
		conns = append(conns, sc)
	}
	s.mu.Unlock()
	for _, sc := range conns {
		if frameType == IPCIncoming && !sc.wants(env) {
			continue
		}
		sc.reply(Frame{Type: frameType, Payload: data})
	}
}

// PushPeerUpdate notifies clients that the peer set changed.
func (s *Server) PushPeerUpdate(peers []PeerInfo) {
	data, err := msgpack.Marshal(peers)
	if err != nil {
		return
	}
	s.mu.Lock()
	conns := make([]*serverConn, 0, len(s.conns))
	for sc := range s.conns {
		conns = append(conns, sc)
	}
	s.mu.Unlock()
	for _, sc := range conns {
		sc.reply(Frame{Type: IPCPeerUpdate, Payload: data})
	}
}

// Close stops accepting and disconnects all clients.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return nil
	}
	s.done = true
	conns := make([]*serverConn, 0, len(s.conns))
	for sc := range s.conns {
		conns = append(conns, sc)
	}
	s.mu.Unlock()
	for _, sc := range conns {
		sc.conn.Close()
	}
	return s.listener.Close()
}

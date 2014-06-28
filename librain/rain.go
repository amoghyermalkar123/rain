package librain

import (
	"crypto/rand"
	"net"
	"sync"
	"time"

	"github.com/cenkalti/log"
	"github.com/cenkalti/rain/internal/logger"
	"github.com/cenkalti/rain/internal/shared"
	"github.com/cenkalti/rain/internal/torrent"
)

// Limits
const (
	maxPeerServe                      = 200
	maxPeerPerTorrent                 = 50
	maxSimultaneoutPieceDownloadStart = 4
	maxSimultaneoutPieceDownloadEnd   = maxPeerPerTorrent
	minSpeedPerTorrent                = 5 * 1024 // KBps
)

// http://www.bittorrent.org/beps/bep_0020.html
var peerIDPrefix = []byte("-RN0001-")

func SetLogLevel(l log.Level) { logger.LogLevel = l }

type Rain struct {
	peerID     shared.PeerID
	listener   *net.TCPListener
	transfers  map[shared.InfoHash]*transfer
	transfersM sync.Mutex
	log        logger.Logger
}

// New returns a pointer to new Rain BitTorrent client.
func New(port int) (*Rain, error) {
	peerID, err := generatePeerID()
	if err != nil {
		return nil, err
	}
	r := &Rain{
		peerID:    peerID,
		transfers: make(map[shared.InfoHash]*transfer),
		log:       logger.New("rain"),
	}
	if err = r.listenPeerPort(port); err != nil {
		return nil, err
	}
	return r, nil
}

func generatePeerID() (shared.PeerID, error) {
	var id shared.PeerID
	copy(id[:], peerIDPrefix)
	_, err := rand.Read(id[len(peerIDPrefix):])
	return id, err
}

// listenPeerPort starts to listen a TCP port to accept incoming peer connections.
func (r *Rain) listenPeerPort(port int) error {
	var err error
	addr := &net.TCPAddr{Port: port}
	r.listener, err = net.ListenTCP("tcp4", addr)
	if err != nil {
		return err
	}
	r.log.Notice("Listening peers on tcp://" + r.listener.Addr().String())
	go r.accepter()
	return nil
}

func (r *Rain) accepter() {
	limit := make(chan struct{}, maxPeerServe)
	for {
		conn, err := r.listener.Accept()
		if err != nil {
			r.log.Error(err)
			return
		}
		limit <- struct{}{}
		go func(conn net.Conn) {
			defer func() {
				if err := recover(); err != nil {
					r.log.Critical(err)
				}
				conn.Close()
				<-limit
			}()
			r.servePeerConn(newPeerConn(conn))
		}(conn)
	}
}

func (r *Rain) port() uint16 { return uint16(r.listener.Addr().(*net.TCPAddr).Port) }

func (r *Rain) servePeerConn(p *peerConn) {
	p.log.Debugln("Serving peer", p.conn.RemoteAddr())

	// Give a minute for completing handshake.
	err := p.conn.SetDeadline(time.Now().Add(time.Minute))
	if err != nil {
		return
	}

	ih, err := p.readHandShake1()
	if err != nil {
		p.log.Error(err)
		return
	}

	// Do not continue if we don't have a torrent with this infoHash.
	r.transfersM.Lock()
	t, ok := r.transfers[*ih]
	if !ok {
		p.log.Error("unexpected info_hash")
		r.transfersM.Unlock()
		return
	}
	r.transfersM.Unlock()

	err = p.sendHandShake(*ih, r.peerID)
	if err != nil {
		p.log.Error(err)
		return
	}

	id, err := p.readHandShake2()
	if err != nil {
		p.log.Error(err)
		return
	}
	if *id == r.peerID {
		p.log.Debug("Rejected own connection: server")
		return
	}

	p.log.Debugln("servePeerConn: Handshake completed", p.conn.RemoteAddr())
	p.run(t)
}

// Download starts a download and waits for it to finish.
func (r *Rain) Download(torrentPath, where string) error {
	torrent, err := torrent.New(torrentPath)
	if err != nil {
		return err
	}
	r.log.Debugf("Parsed torrent file: %#v", torrent)

	t, err := r.newTransfer(torrent, where)
	if err != nil {
		return err
	}

	r.transfersM.Lock()
	r.transfers[torrent.Info.Hash] = t
	r.transfersM.Unlock()

	go t.run()

	<-t.downloaded
	return nil
}

func (r *Rain) DownloadMagnet(url, where string) error {
	panic("not implemented")
}
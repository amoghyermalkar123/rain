package connection_test

import (
	"net"
	"testing"

	"github.com/cenkalti/mse"

	"github.com/cenkalti/rain/internal/connection"
	"github.com/cenkalti/rain/bt"
)

var addr = &net.TCPAddr{
	IP:   net.IPv4(127, 0, 0, 1),
	Port: 5000,
}

var (
	ext1     = [8]byte{0x0A}
	ext2     = [8]byte{0x0B}
	id1      = bt.PeerID{0x0C}
	id2      = bt.PeerID{0x0D}
	infoHash = bt.InfoHash{0x0E}
	sKeyHash = mse.HashSKey(infoHash[:])
)

func TestUnencrypted(t *testing.T) {
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	done := make(chan struct{})
	go func() {
		conn, cipher, ext, id, err := connection.Dial(addr, false, false, ext1, infoHash, id1)
		if err != nil {
			t.Fatal(err)
		}
		if conn == nil {
			t.Errorf("conn: %s", conn)
		}
		if cipher != 0 {
			t.Errorf("cipher: %d", cipher)
		}
		if ext != ext2 {
			t.Errorf("ext: %s", ext)
		}
		if id != id2 {
			t.Errorf("id: %s", id)
		}
		close(done)
	}()
	conn, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	_, cipher, ext, ih, id, err := connection.Accept(conn, nil, false, func(ih bt.InfoHash) bool { return ih == infoHash }, ext2, id2)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if cipher != 0 {
		t.Errorf("cipher: %d", cipher)
	}
	if ext != ext1 {
		t.Errorf("ext: %s", ext)
	}
	if ih != infoHash {
		t.Errorf("ih: %s", ih)
	}
	if id != id1 {
		t.Errorf("id: %s", id)
	}
}

func TestEncrypted(t *testing.T) {
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	done := make(chan struct{})
	go func() {
		conn, cipher, ext, id, err := connection.Dial(addr, true, false, ext1, infoHash, id1)
		if err != nil {
			t.Fatal(err)
		}
		if conn == nil {
			t.Errorf("conn: %s", conn)
		}
		if cipher != mse.RC4 {
			t.Errorf("cipher: %d", cipher)
		}
		if ext != ext2 {
			t.Errorf("ext: %s", ext)
		}
		if id != id2 {
			t.Errorf("id: %s", id)
		}
		_, err = conn.Write([]byte("hello out"))
		if err != nil {
			t.Fail()
		}
		b := make([]byte, 10)
		n, err := conn.Read(b)
		if err != nil {
			t.Error(err)
		}
		if n != 8 {
			t.Fail()
		}
		if string(b[:8]) != "hello in" {
			t.Fail()
		}
		close(done)
	}()
	conn, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	encConn, cipher, ext, ih, id, err := connection.Accept(
		conn,
		func(h [20]byte) (sKey []byte) {
			if h == sKeyHash {
				return infoHash[:]
			}
			return nil
		},
		false,
		func(ih bt.InfoHash) bool { return ih == infoHash },
		ext2, id2)
	if err != nil {
		t.Fatal(err)
	}
	if cipher != mse.RC4 {
		t.Errorf("cipher: %d", cipher)
	}
	if ext != ext1 {
		t.Errorf("ext: %s", ext)
	}
	if ih != infoHash {
		t.Errorf("ih: %s", ih)
	}
	if id != id1 {
		t.Errorf("id: %s", id)
	}
	b := make([]byte, 10)
	n, err := encConn.Read(b)
	if err != nil {
		t.Error(err)
	}
	if n != 9 {
		t.Fail()
	}
	if string(b[:9]) != "hello out" {
		t.Fail()
	}
	_, err = encConn.Write([]byte("hello in"))
	if err != nil {
		t.Fail()
	}
	<-done
}

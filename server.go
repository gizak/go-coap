// Package coap provides a CoAP client and server.
package coap

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

const maxPktLen = 1500

type blkEntry struct {
	data  []byte
	atime int64
	num   int
}

var session map[uint64]blkEntry = make(map[uint64]blkEntry)
var lk sync.Mutex

func addBlockData(tok uint64, num int, d []byte) error {
	// new
	if num == 0 {
		delete(session, tok)
		session[tok] = blkEntry{d, time.Now().Unix(), 0}
		return nil
	}
	// append
	if blk, ok := session[tok]; ok {
		//fmt.Printf("BLOCK session=0x%X num_in=%d num_se=%d\n", tok, num, blk.num)
		if blk.num != num-1 {
			return errors.New(fmt.Sprintf("data order unmatch: session=0x%X num_in=%d num_se=%d", tok, num, blk.num))
		}
		blk.data = append(blk.data, d...)
		blk.atime = time.Now().Unix()
		blk.num = num
		session[tok] = blk // write back changes
		return nil
	}
	// no entry
	return errors.New("no session entry found")
}

// Handler is a type that handles CoAP messages.
type Handler interface {
	// Handle the message and optionally return a response message.
	ServeCOAP(l *net.UDPConn, a *net.UDPAddr, m *Message) *Message
}

type funcHandler func(l *net.UDPConn, a *net.UDPAddr, m *Message) *Message

func (f funcHandler) ServeCOAP(l *net.UDPConn, a *net.UDPAddr, m *Message) *Message {
	return f(l, a, m)
}

// FuncHandler builds a handler from a function.
func FuncHandler(f func(l *net.UDPConn, a *net.UDPAddr, m *Message) *Message) Handler {
	return funcHandler(f)
}

func handlePacket(l *net.UDPConn, data []byte, u *net.UDPAddr,
	rh Handler) {

	msg, err := ParseMessage(data)
	if err != nil {
		log.Printf("Error parsing %v", err)
		return
	}

	// handle block
	blkopt := msg.Option(Block1)
	if blkopt != nil {
		val := blkopt.(uint32)
		//szx := val & 7
		num := val >> 4
		m := (val & 8) >> 3
		//fmt.Printf("%d, %d, %d\n", num, m, 1<<(szx+4))
		res := Message{
			Type:      Acknowledgement,
			Code:      Continue,
			MessageID: msg.MessageID,
			Token:     msg.Token,
		}
		//fmt.Printf("%v",msg.Payload)
		tok, _ := binary.Uvarint(msg.Token)
		if err := addBlockData(tok, int(num), msg.Payload); err != nil {
			fmt.Println(err)
			return
		}
		//fmt.Printf("%v\n",session[tok].data)
		if m != 0 {
			Transmit(l, u, res)
			return
		}
		// last block
		res.Code = Created
		Transmit(l, u, res)
		msg.Payload = make([]byte, len(session[tok].data))
		copy(msg.Payload, session[tok].data)
	}

	rv := rh.ServeCOAP(l, u, &msg)
	if rv != nil {
		rv.Token = msg.Token
		Transmit(l, u, *rv)
	}
}

// Transmit a message.
func Transmit(l *net.UDPConn, a *net.UDPAddr, m Message) error {
	d, err := m.MarshalBinary()
	if err != nil {
		return err
	}

	if a == nil {
		_, err = l.Write(d)
	} else {
		_, err = l.WriteTo(d, a)
	}
	return err
}

// Receive a message.
func Receive(l *net.UDPConn, buf []byte) (Message, error) {
	l.SetReadDeadline(time.Now().Add(ResponseTimeout))

	nr, _, err := l.ReadFromUDP(buf)
	if err != nil {
		return Message{}, err
	}
	return ParseMessage(buf[:nr])
}

// ListenAndServe binds to the given address and serve requests forever.
func ListenAndServe(n, addr string, rh Handler) error {
	uaddr, err := net.ResolveUDPAddr(n, addr)
	if err != nil {
		return err
	}

	l, err := net.ListenUDP(n, uaddr)
	if err != nil {
		return err
	}

	return Serve(l, rh)
}

// Serve processes incoming UDP packets on the given listener, and processes
// these requests forever (or until the listener is closed).
func Serve(listener *net.UDPConn, rh Handler) error {
	buf := make([]byte, maxPktLen)
	for {
		nr, addr, err := listener.ReadFromUDP(buf)
		if err != nil {
			if neterr, ok := err.(net.Error); ok && (neterr.Temporary() || neterr.Timeout()) {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			return err
		}
		tmp := make([]byte, nr)
		copy(tmp, buf)
		go handlePacket(listener, tmp, addr, rh)
	}
}

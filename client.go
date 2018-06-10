package coap

import (
	"net"
	"time"
	"encoding/binary"
	"math/rand"
	"errors"
)

const (
	// ResponseTimeout is the amount of time to wait for a
	// response.
	ResponseTimeout = time.Second * 2
	// ResponseRandomFactor is a multiplier for response backoff.
	ResponseRandomFactor = 1.5
	// MaxRetransmit is the maximum number of times a message will
	// be retransmitted.
	MaxRetransmit = 4
)

// Conn is a CoAP client connection.
type Conn struct {
	conn         *net.UDPConn
	buf          []byte
	BlockSizeExp int
	msWaitTime   int
	curBlock int
}

func (c Conn) BlockSize() int {
	return 1<<byte(c.BlockSizeExp+4)
}

// Dial connects a CoAP client.
func Dial(n, addr string) (*Conn, error) {
	uaddr, err := net.ResolveUDPAddr(n, addr)
	if err != nil {
		return nil, err
	}

	s, err := net.DialUDP("udp", nil, uaddr)
	if err != nil {
		return nil, err
	}

	return &Conn{s, make([]byte, maxPktLen), 4, 10, 0}, nil
}

// Send a message.  Get a response if there is one.
func (c *Conn) Send(req Message) (*Message, error) {

	// switch to block1 if payload is larger then blkLen
	if  len(req.Payload) > c.BlockSize() {
		return c.SendBlock(req)
	}

	// normal message
	return c.send(req)
}

// Send a message.  Get a response if there is one.
func (c *Conn) send(req Message) (*Message, error) {
	err := Transmit(c.conn, nil, req)
	if err != nil {
		return nil, err
	}

	if !req.IsConfirmable() {
		return nil, nil
	}

	rv, err := Receive(c.conn, c.buf)
	if err != nil {
		return nil, err
	}

	return &rv, nil
}


// SendBlock sends a block1 message. Get a resp if there is one
func (c *Conn) SendBlock(req Message) (*Message, error) {
	plen := len(req.Payload)
	blkLen := c.BlockSize()

	tok := make([]byte, 8)
	binary.LittleEndian.PutUint64(tok, rand.Uint64())
	req.Token = tok

	blks := plen/blkLen
	if plen % blkLen != 0 {
		blks++
	}

	payload := make([]byte, plen)
	copy(payload, req.Payload)

	var resp *Message = nil
	var err error = nil

	for i:=0; i< blks; i++ {
		st := i*blkLen
		ed := (i+1)*blkLen
		m := true
		if i == blks -1 {
			m = false
			ed = plen
		}

		ok := false
		req.Payload = payload[st : ed]
		req.SetOption(Block1, BlockOptValue(uint32(i), m, uint32(c.BlockSizeExp)))

		for j:=0; j < MaxRetransmit; j++ {
			resp, err =  c.send(req)
			// if success
			if err == nil && (m && resp.Code==Continue) && (resp.Code & 0xf0) == 2 {
				ok = true
				break
			}
			// else retry
		}
		// return error if reach max retry times
		if !ok {
			return nil, errors.New("Failed to send block message")
		}
	}
	return resp, err

}

// Receive a message.
func (c *Conn) Receive() (*Message, error) {
	rv, err := Receive(c.conn, c.buf)
	if err != nil {
		return nil, err
	}
	return &rv, nil
}

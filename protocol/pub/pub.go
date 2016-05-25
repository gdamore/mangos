// Copyright 2015 The Mangos Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use file except in compliance with the License.
// You may obtain a copy of the license at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package pub implements the PUB protocol.  This protocol publishes messages
// to subscribers (SUB peers).  The subscribers will filter incoming messages
// from the publisher based on their subscription.
package pub

import (
	"sync"
	"time"

	"github.com/go-mangos/mangos"
)

type pubEp struct {
	ep mangos.Endpoint
	q  chan *mangos.Message
	p  *pub
	w  mangos.Waiter
}

type pub struct {
	sock mangos.ProtocolSocket
	eps  map[uint32]*pubEp
	raw  bool
	w    mangos.Waiter
	init sync.Once

	sync.Mutex
}

func (p *pub) Init(sock mangos.ProtocolSocket) {
	p.sock = sock
	p.eps = make(map[uint32]*pubEp)
	p.sock.SetRecvError(mangos.ErrProtoOp)
	p.w.Init()
}

func (p *pub) Shutdown(expire time.Time) {

	p.w.WaitAbsTimeout(expire)

	p.Lock()
	peers := p.eps
	p.eps = make(map[uint32]*pubEp)
	p.Unlock()

	for id, peer := range peers {
		mangos.DrainChannel(peer.q, expire)
		close(peer.q)
		delete(peers, id)
	}
}

// Bottom sender.
func (pe *pubEp) peerSender() {

	for {
		m := <-pe.q
		if m == nil {
			break
		}

		if pe.ep.SendMsg(m) != nil {
			m.Free()
			break
		}
	}
}

// Top sender.
func (p *pub) sender() {
	defer p.w.Done()

	sq := p.sock.SendChannel()
	cq := p.sock.CloseChannel()

	for {
		select {
		case <-cq:
			return

		case m := <-sq:

			p.Lock()
			for _, peer := range p.eps {
				m := m.Dup()
				select {
				case peer.q <- m:
				default:
					m.Free()
				}
			}
			p.Unlock()
		}
	}
}

func (p *pub) AddEndpoint(ep mangos.Endpoint) {
	p.init.Do(func() {
		p.w.Add()
		go p.sender()
	})
	depth := 16
	if i, err := p.sock.GetOption(mangos.OptionWriteQLen); err == nil {
		depth = i.(int)
	}
	pe := &pubEp{ep: ep, p: p, q: make(chan *mangos.Message, depth)}
	pe.w.Init()
	p.Lock()
	p.eps[ep.GetID()] = pe
	p.Unlock()

	pe.w.Add()
	go pe.peerSender()
	go mangos.NullRecv(ep)
}

func (p *pub) RemoveEndpoint(ep mangos.Endpoint) {
	id := ep.GetID()
	p.Lock()
	pe := p.eps[id]
	delete(p.eps, id)
	p.Unlock()
	if pe != nil {
		close(pe.q)
	}
}

func (*pub) Number() mangos.ProtocolNumber {
	return mangos.ProtoPub
}

func (*pub) PeerNumber() mangos.ProtocolNumber {
	return mangos.ProtoSub
}

func (p *pub) SetOption(name string, v interface{}) error {
	var ok bool
	switch name {
	case mangos.OptionRaw:
		if p.raw, ok = v.(bool); !ok {
			return mangos.ErrBadValue
		}
		return nil
	default:
		return mangos.ErrBadOption
	}
}

func (p *pub) GetOption(name string) (interface{}, error) {
	switch name {
	case mangos.OptionRaw:
		return p.raw, nil
	default:
		return nil, mangos.ErrBadOption
	}
}

// NewSocket allocates a new Socket using the PUB protocol.
func NewSocket() (mangos.Socket, error) {
	return mangos.MakeSocket(&pub{}), nil
}

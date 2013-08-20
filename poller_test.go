/*
	Copyright (C) 2013 Ondrej Kupka
	Copyright (C) 2013 Contributors as noted in the AUTHORS file

	Permission is hereby granted, free of charge, to any person obtaining a copy
	of this software and associated documentation files (the "Software"),
	to deal in the Software without restriction, including without limitation
	the rights to use, copy, modify, merge, publish, distribute, sublicense,
	and/or sell copies of the Software, and to permit persons to whom
	the Software is furnished to do so, subject to the following conditions:

	The above copyright notice and this permission notice shall be included
	in all copies or substantial portions of the Software.

	THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
	IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
	FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL
	THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
	LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING
	FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS
	IN THE SOFTWARE.
*/

package poller

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

import (
	zmq "github.com/alecthomas/gozmq"
)

// Tests ----------------------------------------------------------------------

func TestPoller_SingleSendAndPoll(test *testing.T) {
	factory, err := NewSocketFactory()
	if err != nil {
		test.Fatal(err)
	}
	defer factory.Close()

	in, out, err := factory.NewPipe()
	if err != nil {
		test.Fatal(err)
	}

	err = in.Send([]byte{42}, 0)
	if err != nil {
		test.Fatal(err)
	}

	poller, err := New(factory.ctx)
	if err != nil {
		test.Fatal(err)
	}

	pollCh := make(chan *PollResult, 1)
	err = poller.Poll(zmq.PollItems{
		{
			Socket: out,
			Events: zmq.POLLIN,
		},
	}, pollCh)
	if err != nil {
		test.Fatal(err)
	}

	timeout := time.After(time.Second)

	select {
	case <-pollCh:
		msg, err := out.Recv(0)
		if err != nil {
			test.Fatal(err)
		}
		if msg[0] != 42 {
			test.Fatal("received != expected")
		}
	case <-timeout:
		test.Fatal("Test timed out.")
	}

	exitCh := make(chan struct{})
	err = poller.Close(exitCh)
	if err != nil {
		test.Fatal(err)
	}

	timeout = time.After(time.Second)

	select {
	case <-exitCh:
		return
	case <-timeout:
		test.Error("Test timed out.")
	}
}

// Benchmarks -----------------------------------------------------------------

func BenchmarkPoller_Raw0MQ(b *testing.B) {
	factory, err := NewSocketFactory()
	if err != nil {
		b.Fatal(err)
	}
	defer factory.Close()

	in, out, err := factory.NewPipe()
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := in.Send([]byte{0}, 0); err != nil {
			b.Fatal(err)
		}
		if _, err := out.Recv(0); err != nil {
			b.Fatal(err)
		}
	}

	b.StopTimer()
}

func BenchmarkPoller_Poller(b *testing.B) {
	factory, err := NewSocketFactory()
	if err != nil {
		b.Fatal(err)
	}
	defer factory.Close()

	in, out, err := factory.NewPipe()
	if err != nil {
		b.Fatal(err)
	}

	poller, ex := New(factory.ctx)
	if ex != nil {
		b.Error(ex)
		return
	}
	defer poller.Close(nil)

	pollCh := make(chan *PollResult, 1)
	ex = poller.Poll(zmq.PollItems{
		{
			Socket: out,
			Events: zmq.POLLIN,
		},
	}, pollCh)
	if ex != nil {
		b.Error(ex)
		return
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := in.Send([]byte{0}, 0); err != nil {
			b.Fatal(err)
		}

		<-pollCh
		if err := poller.Continue(); err != nil {
			b.Fatal(err)
		}

		if _, err := out.Recv(0); err != nil {
			b.Fatal(err)
		}
	}

	b.StopTimer()
}

/**
 * SocketFactory to easily create sockets and close 0MQ
 */

type SocketFactory struct {
	ctx         *zmq.Context
	ss          []*zmq.Socket
	pipeCounter int
}

func NewSocketFactory() (sf *SocketFactory, err error) {
	ctx, err := zmq.NewContext()
	if err == nil {
		sf = &SocketFactory{ctx, make([]*zmq.Socket, 0), 0}
	}
	return
}

func (self *SocketFactory) NewSocket(t zmq.SocketType) (*zmq.Socket, error) {
	if self.ctx == nil {
		return nil, errors.New("SocketFactory has been closed.")
	}
	s, err := self.ctx.NewSocket(t)
	if err != nil {
		return nil, err
	}
	self.ss = append(self.ss, s)
	return s, nil
}

func (self *SocketFactory) NewPipe() (in *zmq.Socket, out *zmq.Socket, err error) {
	in, err = self.NewSocket(zmq.PAIR)
	if err != nil {
		return
	}
	out, err = self.NewSocket(zmq.PAIR)
	if err != nil {
		return
	}

	endpoint := fmt.Sprintf("inproc://pipe%d", self.pipeCounter)
	self.pipeCounter++

	// Leave the sockets to be collected by factory.Close().
	err = out.Bind(endpoint)
	if err != nil {
		return
	}
	err = in.Connect(endpoint)
	if err != nil {
		return
	}

	return
}

func (self *SocketFactory) Close() {
	for _, s := range self.ss {
		s.Close()
	}
	self.ctx.Close()
	self.ctx = nil
}

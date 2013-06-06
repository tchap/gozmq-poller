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

import zmq "github.com/alecthomas/gozmq"

func TestSendAndPoll(test *testing.T) {
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

	poller, err := New(factory.ctx, 0)
	if err != nil {
		test.Fatal(err)
	}

	ch, err := poller.Poll(zmq.PollItems{zmq.PollItem{
		Socket: out,
		Events: zmq.POLLIN,
	}})
	if err != nil {
		test.Fatal(err)
	}

	timeout := time.After(time.Second)

	select {
	case <-ch:
		msg, err := out.Recv(0)
		if err != nil {
			test.Fatal(err)
		}
		if msg[0] != 42 {
			test.Fatal(errors.New("reveived != expected"))
		}
	case <-timeout:
		test.Fatal(errors.New("Test timed out."))
	}

	ch, err = poller.Close()
	if err != nil {
		test.Fatal(err)
	}

	timeout = time.After(time.Second)

	select {
	case leftovers := <-ch:
		if leftovers.Count > 0 || len(leftovers.Items) > 0 {
			test.Fatal(errors.New("Unexpected leftovers received."))
		}
	case <-timeout:
		test.Fatal(errors.New("Test timed out."))
	}
}

func TestPoller(test *testing.T) {
	factory, err := NewSocketFactory()
	if err != nil {
		test.Fatal(err)
	}
	defer factory.Close()

	in, out, err := factory.NewPipe()
	if err != nil {
		test.Fatal(err)
	}

	data := make([]byte, 1000)
	for i := range data {
		data[i] = 1
	}

	go func() {
		for _, d := range data {
			err := in.Send([]byte{d}, 0)
			if err != nil {
				test.Error(err)
				return
			}
		}
	}()

	exit := make(chan bool, 1)

	go func() {
		defer func() {
			exit <- true
		}()

		poller, ex := New(factory.ctx, 0)
		if ex != nil {
			test.Error(ex)
			return
		}

		pollChan, ex := poller.Poll([]zmq.PollItem{zmq.PollItem{
			Socket: out,
			Events: zmq.POLLIN,
		}})
		if ex != nil {
			test.Error(ex)
			return
		}

		for _, d := range data {
			res := <-pollChan
			if res.Error != nil {
				test.Error(res.Error)
				return
			}
			msg, ex := out.Recv(0)
			if ex != nil {
				test.Error(ex)
				return
			}
			if msg[0] != d {
				test.Error(errors.New("received != expected"))
				return
			}
			ex = poller.Continue()
			if ex != nil {
				test.Error(ex)
				return
			}
		}

		leftovers, ex := poller.Close()
		if ex != nil {
			test.Error(ex)
			return
		}
		<-leftovers
	}()

	timeout := time.After(time.Second)

	select {
	case <-exit:
		break
	case <-timeout:
		test.Error(errors.New("Test timed out."))
	}
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

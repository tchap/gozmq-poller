/*
	Copyright (C) 2013 Ondrej Kupka

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

package main

import (
	"fmt"
)

import (
	zmq "github.com/alecthomas/gozmq"
	poller "github.com/tchap/gozmq-poller"
)

func main() {
	ctx, _ := zmq.NewContext()
	defer ctx.Close()
	out, _ := ctx.NewSocket(zmq.PULL)
	defer out.Close()
	in, _ := ctx.NewSocket(zmq.PUSH)
	defer in.Close()

	out.Bind("inproc://pipe")
	in.Connect("inproc://pipe")

	f := poller.NewFactory(ctx)
	p, _ := f.NewPoller(zmq.PollItems{
		{
			Socket: out,
			Events: zmq.POLLIN,
		},
	})

	pollCh := p.Poll()

	in.Send([]byte{0}, 0)

	<-pollCh
	out.Recv(0)
	fmt.Println("Data received")

	p.Close()
}

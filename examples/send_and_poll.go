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
	gzu "github.com/tchap/gozmq-utils"
)

func main() {
	ctx, _ := zmq.NewContext()
	in, _ := ctx.NewSocket(zmq.PUSH)
	out, _ := ctx.NewSocket(zmq.PULL)

	out.Bind("inproc://pipe")
	in.Connect("inproc://pipe")

	in.Send([]byte{0}, 0)

	p, _ := gzu.NewPoller(ctx, 0)
	ch, _ := p.Poll(zmq.PollItems{zmq.PollItem{
		Socket: out,
		Events: zmq.POLLIN,
	}})

	<-ch
	out.Recv(0)

	wait, _ := p.Close()
	<-wait

	fmt.Println("Data received")
	in.Close()
	out.Close()
	ctx.Close()
}

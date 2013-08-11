# About gozmq Poller

[![Build
Status](https://travis-ci.org/tchap/gozmq-poller.png?branch=master)](https://travis-ci.org/tchap/gozmq-poller)

`gozmq.Poll` function is useful, but it is not particularly Go-friendly.
You very rarely want to block on a function call, you rather want to `select`
on a bunch of channels. And that is where this Poller comes into play. It wraps
`gozmq.Poll` and lets you wait on a channel.

There is another advantage in using Poller - you can stop the polling without
waiting for its timeout. This is not possible with `gozmq.Poll`, or rather you
have to write additional code, and that is exactly what Poller does for you.

# Installation

This is just yet another GitHub repository which you can directly import into
your project:
```go
import poller "github.com/tchap/gozmq-poller"
```
Do not forget to issue `go get` to download the package.

# Example

```go
package main

import "fmt"

import (
	zmq "github.com/alecthomas/gozmq"
	poller "github.com/tchap/gozmq-poller"
)

func main() {
	ctx, _ := zmq.NewContext()
	in, _ := ctx.NewSocket(zmq.PUSH)
	out, _ := ctx.NewSocket(zmq.PULL)

	out.Bind("inproc://pipe")
	in.Connect("inproc://pipe")

	in.Send([]byte{0}, 0)

	p, _ := poller.New(ctx)
	ch, _ := p.Poll(zmq.PollItems{zmq.PollItem{
		Socket: out,
		Events: zmq.POLLIN,
	}})

	<-ch
	out.Recv(0)

	leftovers, _ := p.Close()
	<-leftovers

	fmt.Println("Data received")
	in.Close()
	out.Close()
	ctx.Close()
}
```

# Caveats

Make sure to wait on the channel returned by `Poller.Close` before calling your `gozmq.Context.Close`.
If you don't do that, you are risking getting blocked indefinitely in the call to `gozmq.Context.Close`
since the sockets may not be closed properly at that time.

# Documentation

We are writing Go, so of course there is some generated
[Godoc documentation](http://godoc.org/github.com/tchap/gozmq-poller)
available.

# Contributing

As all open source projects, we welcome any reasonable contributions,
particularly bug fixes :-) Since this project is rather small, it is
questionable if there is really anything to add. There is always some space for
improvements, though :-)

# License

See the `COPYING` file.

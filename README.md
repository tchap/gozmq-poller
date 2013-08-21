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

# State of the Project

I am still developing this, so things may and will change if I find it more
appropriate for my use cases.

# Installation

This is just yet another GitHub repository which you can directly import into
your project:
```go
import poller "github.com/tchap/gozmq-poller"
```
Do not forget to issue `go get` to download the package.

# Examples

Check the `examples` directory to see some examples of how to use Poller.

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

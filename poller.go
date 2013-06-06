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

package gzu

import (
	"errors"
	"sync"
)

import zmq "github.com/alecthomas/gozmq"

/**
 * Poller
 */

// Constants

const (
	statePolling = iota
	stateClosed
)

const (
	cmdPoll = iota
	cmdStop
	cmdContinue
	cmdClose
)

const (
	interruptEndpoint    = "inproc://gozmqPoller_8DUvkrWQYP"
	defaultCmdChannelLen = 10
)

// Structs

type Poller struct {
	state int

	// For sending interrupts
	intIn  *zmq.Socket
	intOut *zmq.Socket

	// For exchanging commands
	cmdChan chan *command

	// For returning results
	pollChan chan *PollResult

	// For signalizing exit
	exitChan chan bool

	lock sync.Mutex
}

type command struct {
	Cmd  int
	Args interface{}
}

type PollResult struct {
	Count int
	Items zmq.PollItems
	Error error
}

// Constructor

func New(ctx *zmq.Context, optChanLen int) (p *Poller, err error) {
	var cmdChanLen int

	in, err := ctx.NewSocket(zmq.PAIR)
	if err != nil {
		return
	}
	out, err := ctx.NewSocket(zmq.PAIR)
	if err != nil {
		goto closeIn
	}

	err = out.Bind(interruptEndpoint)
	if err != nil {
		goto closeBoth
	}
	err = in.Connect(interruptEndpoint)
	if err != nil {
		goto closeBoth
	}

	if optChanLen > 0 {
		cmdChanLen = optChanLen
	} else {
		cmdChanLen = defaultCmdChannelLen
	}

	p = &Poller{
		state:    statePolling,
		intIn:    in,
		intOut:   out,
		cmdChan:  make(chan *command, cmdChanLen),
		pollChan: make(chan *PollResult),
		exitChan: make(chan bool, 1),
	}

	go p.worker()
	return

closeBoth:
	out.Close()
closeIn:
	in.Close()
	return
}

// Commands

func (self *Poller) Poll(items zmq.PollItems) (<-chan *PollResult, error) {
	self.lock.Lock()
	defer self.lock.Unlock()

	if self.state == stateClosed {
		return nil, errors.New("Poller already closed.")
	}

	close(self.pollChan)
	self.pollChan = make(chan *PollResult)

	err := self.command(&command{cmdPoll, &items})
	if err != nil {
		return nil, err
	}
	return self.pollChan, nil
}

func (self *Poller) Stop() (err error) {
	self.lock.Lock()
	defer self.lock.Unlock()

	if self.state == stateClosed {
		return errors.New("Poller already closed.")
	}

	return self.command(&command{cmdStop, nil})
}

func (self *Poller) Continue() (err error) {
	self.lock.Lock()
	defer self.lock.Unlock()

	if self.state == stateClosed {
		return errors.New("Poller already closed.")
	}

	return self.command(&command{cmdContinue, nil})
}

func (self *Poller) Close() (resChan <-chan *PollResult, err error) {
	self.lock.Lock()
	defer self.lock.Unlock()

	if self.state == stateClosed {
		err = errors.New("Poller already closed.")
		return
	}

	ch := make(chan *PollResult, 1)
	err = self.command(&command{cmdClose, ch})
	if err == nil {
		resChan = ch
	}
	return
}

func (self *Poller) IsClosed() bool {
	self.lock.Lock()
	defer self.lock.Unlock()

	return self.state == stateClosed
}

// Commands helper functions

func (self *Poller) command(cmd *command) (err error) {
	self.cmdChan <- cmd
	return self.interrupt()
}

func (self *Poller) interrupt() (err error) {
	return self.intIn.Send([]byte{0}, 0)
}

// Main worker goroutine

func (self *Poller) worker() {
	intItem := zmq.PollItem{
		Socket: self.intOut,
		Events: zmq.POLLIN,
	}

	items := zmq.PollItems{intItem}
	var lastItems zmq.PollItems

	// When zmq.Poll fails, remove all the fds except intItem and set this flag.
	// If zmq.Poll fails during the next iteration as well, exit the loop.
	nuke := false

	for {
		// Poll on the pollItems
		rc, err := zmq.Poll(items, -1)
		if err != nil {
			self.pollChan <- &PollResult{rc, nil, err}
			if nuke {
				self.finalize(nil)
				return
			}
			nuke = true
			items = zmq.PollItems{intItem}
			continue
		}
		nuke = false

		// Just forward the return value if there is no interrupt
		if items[len(items)-1].REvents&zmq.POLLIN == 0 {
			self.pollChan <- &PollResult{rc, items[:len(items)-1], nil}
			// Poll for commands only until Continue() is called
			items = zmq.PollItems{intItem}
			continue
		}

		// Process single interrupt
		_, err = intItem.Socket.Recv(0)
		if err != nil {
			self.pollChan <- &PollResult{-1, nil, err}
			return
		}

		cmd := <-self.cmdChan

		switch cmd.Cmd {
		case cmdPoll:
			lastItems = *(cmd.Args.(*zmq.PollItems))
			items = append(lastItems, intItem)
		case cmdStop:
			items = []zmq.PollItem{intItem}
		case cmdContinue:
			if lastItems != nil {
				items = append(lastItems, intItem)
			} else {
				items = []zmq.PollItem{intItem}
			}
		case cmdClose:
			// Return what was signaled at the moment of the interrupt
			self.finalize(func() {
				resChan := cmd.Args.(chan *PollResult)
				resChan <- &PollResult{rc - 1, items[:len(items)-1], nil}
				close(resChan)
			})
			return
		default:
			panic("Poller received an unknown command.")
		}
	}
}

func (self *Poller) finalize(exitCallback func()) {
	self.lock.Lock()
	defer self.lock.Unlock()

	// Close channels
	close(self.cmdChan)
	close(self.pollChan)

	// Close the interrupt sockets
	self.intIn.Close()
	self.intOut.Close()

	// Mark the poller as closed
	self.state = stateClosed

	// Signal exit
	if exitCallback != nil {
		exitCallback()
	}
}

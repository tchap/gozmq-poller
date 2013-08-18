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

// Package gozmq-poller turns polling on ZeroMQ socket descriptors into
// selecting on channels, thus making the whole thing much more Go-friendly.
package poller

import (
	"errors"
	"sync"
)

import (
	sm "github.com/tchap/go-statemachine"
	log "github.com/cihub/seelog"
	zmq "github.com/alecthomas/gozmq"
)

const (
	interruptEndpoint = "inproc://gozmqPoller_8DUvkrWQYP"
)

// PUBLIC TYPES ---------------------------------------------------------------

type Poller struct {
	// Internal state machine
	sm *sm.StateMachine

	// For sending interrupts
	intIn  *zmq.Socket
	intOut *zmq.Socket

	// A logger that is being used if set. Set this before any other action.
	Logger log.LoggerInterface
}

// PollResult is sent back to the user once polling returns.
type PollResult struct {
	Count int
	Items zmq.PollItems
	Error error
}

// STATES & EVENTS ------------------------------------------------------------

const (
	stateInitial = iota
	statePolling
	statePaused
	stateClosed
)

const (
	cmdPoll = iota
	cmdPause
	cmdContinue
	cmdClose
)

// CONSTRUCTOR & METHODS ------------------------------------------------------

// Poller constructor
func New(ctx *zmq.Context) (p *Poller, err error) {
	// Create and connect the internal interrupt sockets.
	in, err := ctx.NewSocket(zmq.PAIR)
	if err != nil {
		return
	}
	out, err := ctx.NewSocket(zmq.PAIR)
	if err != nil {
		in.Close()
		return
	}

	err = out.Bind(interruptEndpoint)
	if err != nil {
		in.Close()
		out.Close()
		return
	}
	err = in.Connect(interruptEndpoint)
	if err != nil {
		in.Close()
		out.Close()
		return
	}

	// Create and inititalise the internal state machine.
	psm := sm.New(stateInitial, 4, 4, 0)

	p =: &Poller{
		sm:       psm,
		intIn:    in,
		intOut:   out,
	}

	// POLL
	psm.On(cmdPoll, stateInitial, p.handlePoll)
	psm.On(cmdPoll, statePolling, p.handlePoll)
	psm.On(cmdPoll, statePaused, p.handlePoll)

	// PAUSE
	psm.On(cmdPause, statePolling, p.handlePause)

	// CONTINUE
	psm.On(cmdContinue, statePaused, p.handleContinue)

	// CLOSE
	psm.On(cmdClose, stateInitial, p.handleClose)
	psm.On(cmdClose, statePolling, p.handleClose)
	psm.On(cmdClose, statePaused, p.handleClose)

	return p
}

// Poll -----------------------------------------------------------------------

type pollArgs struct {
	items  zmq.PollItems
	pollCh chan<- *PollResult
}

// Poll starts polling on the set of socket descriptors.
func (self *Poller) Poll(items zmq.PollItems, pollCh chan<- *PollResult) error {
	if len(items) == 0 {
		panic("Poll items are empty")
	}
	if pollCh == nil {
		panic("Poll channel is nil")
	}

	errCh := make(chan error, 1)
	self.sm.Emit(&sm.Event{
		cmdPoll,
		&pollArgs{items, pollCh},
	}, errCh)
	return <-errCh
}

func (self *Poller) handlePoll(s sm.State, e *sm.Event) (next sm.State) {
	args := e.Data.(*pollArgs)
	if s == statePolling {
		self.interruptPoll()
		close(self.pollCh)
	}
	self.items = args.items
	self.pollCh = args.pollCh
	go self.pollLoop()
	return statePolling
}

// Pause ----------------------------------------------------------------------

// Pause will pause polling until Continue is called again. This call breaks
// the call to gozmq.Poll and makes the poller wait for more commands to come,
// no other descriptors.
//
// If pauseCh is not nil, it will be closed when the poll call is interrupted
// to signal that the 0MQ sockets are no longer in use.
func (self *Poller) Pause(pauseCh chan struct{}) error {
	errCh := make(chan error, 1)
	self.sm.Emit(&sm.Event{
		cmdPause,
		pauseCh,
	}, errCh)
	return <-errCh
}

func (self *Poller) handlePause(s sm.State, e *sm.Event) (next sm.State) {
	pauseCh := e.Data.(chan struct{})
	self.interruptPoll()
	if pauseCh != nil {
		close(pauseCh)
	}
	return statePaused
}

// Continue -------------------------------------------------------------------

// Continue resumes the poller after a call to Pause or after the polling is
// successful. The poller is automaticall paused before returning a PollResult,
// otherwise gozmq.Poll would keep returning again and again until the user
// reads what is available on the descriptors passed to the poller. We want to
// prevent such a busy-waiting and flooding of the result channel.
func (self *Poller) Continue() error {
	errCh := make(chan error, 1)
	self.sm.Emit(&sm.Event{
		cmdContinue,
		nil
	}, errCh)
	return <-errCh
}

func (self *Poller) handleContinue(s sm.State, e *sm.Event) (next sm.State) {
	go self.pollLoop()
	return statePolling
}

// Close ----------------------------------------------------------------------

// Close the poller and release all internal 0MQ sockets.
func (self *Poller) Close(closeCh chan struct{}) error {
	errCh := make(chan error, 1)
	self.sm.Emit(&sm.Event{
		cmdClose,
		closeCh,
	}, errCh)
	return <-errCh
}

func (self *Poller) handleClose(s sm.State, e *sm.Event) (next sm.State) {
	closeCh := e.Data.(chan struct{})

	if s == statePolling {
		self.interruptPoll()
		self.items = nil
		close(self.pollCh)
	}

	err := self.intIn.Close()
	if err != nil && self.logger != nil {
		self.logger.Warn(err)
	}
	err = self.intOut.Close()
	if err != nil && self.logger != nil {
		self.logger.Warn(err)
	}

	self.sm.Terminate()
	<-self.sm.TerminatedChannel()

	if closeCh != nil {
		close(closeCh)
	}
}

// HELPERS --------------------------------------------------------------------

// Interrupt the call to gozmq.Poll().
func (self *Poller) interrupt() error {
	return self.intIn.Send([]byte{0}, 0)
}

// Internal poll loop goroutine
func (self *Poller) pollLoop() {
	intItem := zmq.PollItem{
		Socket: self.intOut,
		Events: zmq.POLLIN,
	}

	items = append(self.items, intItem)

	for {
		// Poll on the poll items indefinitely.
		rc, err := zmq.Poll(items, -1)
		if err != nil {
			if self.logger != nil {
				self.logger.Error(err)
			}
			self.pollCh <- &PollResult{rc, nil, err}
			close(self.pollCh)
			self.pollCh = nil
		}

		// Just forward the return value if there is no interrupt.
		if items[len(items)-1].REvents&zmq.POLLIN == 0 {
			self.pollCh <- &PollResult{rc, items[:len(items)-1], nil}
			// Poll for commands only until Continue() is called.
			items = zmq.PollItems{intItem}
			continue
		}

		// Interrupt received, exiting...
		_, err = intItem.Socket.Recv(0)
		if err != nil {
			self.pollChan <- &PollResult{-1, nil, err}
			return
		}
	}
}

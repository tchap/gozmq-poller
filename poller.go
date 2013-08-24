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
	zmq "github.com/alecthomas/gozmq"
	sm "github.com/tchap/go-statemachine"
)

const (
	interruptEndpoint = "inproc://gozmqPoller_8DUvkrWQYP"
)

// STRUCT: PollerFactory ------------------------------------------------------

type PollerFactory struct {
	ctx *zmq.Context
}

func NewPollerFactory(ctx *zmq.Context) *PollerFactory {
	return &PollerFactory{ctx}
}

func (factory *PollerFactory) NewPoller(items zmq.PollItems) (p *Poller, err error) {
	// Create and connect the internal interrupt sockets.
	in, err := factory.ctx.NewSocket(zmq.PAIR)
	if err != nil {
		return
	}
	out, err := factory.ctx.NewSocket(zmq.PAIR)
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
	psm := sm.New(stateInitialised, 4, 4)

	p = &Poller{
		sm:         psm,
		intIn:      in,
		intOut:     out,
		items:      items,
		pollCh:     make(chan *PollResult, 1),
		continueCh: make(chan bool, 1),
	}

	// Command: WITHPAUSED
	psm.On(cmdWithPaused, []sm.State{
		statePolling,
		statePaused,
	}, p.handleWithPausedCommand)

	// Command: CONTINUE
	psm.On(cmdContinue, []sm.State{
		stateInitialised,
		statePolling,
		statePaused,
	}, p.handleContinueCommand)

	// Command: CLOSE
	psm.On(cmdClose, []sm.State{
		stateInitialised,
		statePolling,
		statePaused,
	}, p.handleCloseCommand)

	// Event: POLLRETURNED
	psm.On(evtPollReturned, []sm.State{
		statePolling,
	}, p.handlePollEvent)

	return
}

// STRUCT: Poller -------------------------------------------------------------

type Poller struct {
	// Internal state machine
	sm *sm.StateMachine

	// Internal stuff for managing the internal goroutine
	intIn      *zmq.Socket
	intOut     *zmq.Socket
	intCh      chan struct{}
	continueCh chan bool

	// Polling stuff
	items  zmq.PollItems
	pollCh chan *PollResult
}

// PollResult is sent back to the user once polling returns.
type PollResult struct {
	Count int
	Items zmq.PollItems
	Error error
}

// States & Events ------------------------------------------------------------

const (
	stateInitialised = iota
	statePolling
	statePaused
	stateClosed
)

const (
	cmdWithPaused = iota
	cmdContinue
	cmdClose

	evtPollReturned
)

// Poll

func (self *Poller) Poll() (pollCh <-chan *PollResult) {
	return self.pollCh
}

// Command: WITHPAUSED --------------------------------------------------------

// Run the closure and make sure the poller is paused while doing so. This is
// userful if you want to send some data to one of the sockets being polled since
// you cannot do both polling and sending. 0MQ sockets are not supposed to be used
// from multiple threads at once.
func (self *Poller) WithPaused(closure func(err error)) error {
	return self.sm.Emit(&sm.Event{
		cmdWithPaused,
		closure,
	})
}

func (self *Poller) handleWithPausedCommand(s sm.State, e *sm.Event) sm.State {
	closure := e.Data.(func(err error))

	if s == statePolling {
		if err := self.interruptPolling(); err != nil {
			closure(err)
			return s
		}
	}

	closure(nil)

	if s == statePolling {
		self.continueCh <- true
	}

	return s
}

// Command: CONTINUE ----------------------------------------------------------

// The poller is automatically paused before returning a PollResult on the polling
// channel. This method is to be called after all the data are read from the sockets
// and we are ready to poll for more messages.
//
// If polling was not paused, gozmq.Poll would just keep returning again and again
// until the user reads what is available on the descriptors passed to the poller.
// We want to prevent such a busy waiting and flooding of the polling channel.
func (self *Poller) Continue() error {
	return self.sm.Emit(&sm.Event{
		cmdContinue,
		nil,
	})
}

func (self *Poller) handleContinueCommand(s sm.State, e *sm.Event) sm.State {
	switch s {
	case stateInitialised:
		go self.poll()
	case statePolling:
		break
	case statePaused:
		self.continueCh <- true
	}
	return statePolling
}

// Command: CLOSE -------------------------------------------------------------

// Close the poller and release all internal 0MQ sockets. This method blocks
// until all work is done.
func (self *Poller) Close() error {
	errCh := make(chan error, 1)
	if err := self.sm.Emit(&sm.Event{
		cmdClose,
		errCh,
	}); err != nil {
		return err
	}
	return <-errCh
}

func (self *Poller) handleCloseCommand(s sm.State, e *sm.Event) sm.State {
	errCh := e.Data.(chan error)

	if s == stateInitialised {
		self.intIn.Close()
		self.intOut.Close()
		close(self.pollCh)
		close(errCh)
		return stateClosed
	}

	// Signal the internal goroutine to exit next time it gets blocked on the
	// continue channel. It could be already blocked there, so this may as well
	// just cause it to return right here.
	close(self.continueCh)

	// Break gozmq.Poll() if necessary. Use 'go' since we might not be blocked
	// in Poll() and this call could block the whole thing.
	if s == statePolling {
		go func() {
			if err := self.interruptPolling(); err != nil {
				errCh <- err
				close(errCh)
				errCh = nil
			}
		}()
	}

	go func() {
		// Wait for the polling goroutine to exit. This can be detected by
		// waiting on the state machine's terminated channel.
		<-self.sm.TerminatedChannel()

		self.intIn.Close()
		self.intOut.Close()

		close(self.pollCh)

		// Signal that we are done with all the cleanup.
		close(errCh)
	}()

	return stateClosed
}

// Event: POLL ----------------------------------------------------------------

func (self *Poller) handlePollEvent(s sm.State, e *sm.Event) sm.State {
	self.pollCh <- e.Data.(*PollResult)
	return statePaused
}

// Helpers --------------------------------------------------------------------

// Interrupt gozmq.Poll().
func (self *Poller) interruptPolling() error {
	self.intCh = make(chan struct{})

	if err := self.intIn.Send([]byte{0}, 0); err != nil {
		self.intCh = nil
		return err
	}

	// Block until gozmq.Poll() is interrupted or the poller is closed.
	select {
	case <-self.intCh:
		break
	case <-self.sm.TerminatedChannel():
		break
	}

	self.intCh = nil
	return nil
}

// Internal polling goroutine
func (self *Poller) poll() {
	items := append(self.items, zmq.PollItem{
		Socket: self.intOut,
		Events: zmq.POLLIN,
	})

	for {
		// Poll on the poll items indefinitely.
		rc, err := zmq.Poll(items, -1)
		if err != nil {
			self.sm.Emit(&sm.Event{
				evtPollReturned,
				&PollResult{rc, nil, err},
			})
		} else {
			if items[len(items)-1].REvents&zmq.POLLIN == 0 {
				// A user-defined socket is available.
				self.sm.Emit(&sm.Event{
					evtPollReturned,
					&PollResult{rc, items[:len(items)-1], nil},
				})
			} else {
				// An interrupt arrived. Receive it and send some feedback.
				if _, err := self.intOut.Recv(0); err != nil {
					panic(err)
				}
				close(self.intCh)
			}
		}

		// Wait for CONTINUE.
		_, ok := <-self.continueCh
		if !ok {
			self.sm.Terminate()
			return
		}
	}
}

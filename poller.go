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
	log "github.com/cihub/seelog"
	sm "github.com/tchap/go-statemachine"
)

const (
	interruptEndpoint = "inproc://gozmqPoller_8DUvkrWQYP"
)

// EXPORTED TYPES -------------------------------------------------------------

type Poller struct {
	// Internal state machine
	sm *sm.StateMachine

	// Internal stuff for managing the internal goroutine
	intIn  *zmq.Socket
	intOut *zmq.Socket
	intCh  chan struct{}
	cmdCh  chan sm.State

	// Placeholders for various arguments to be passed to the internal goroutine.
	items  zmq.PollItems
	pollCh chan<- *PollResult

	// A logger that is being used. seelog.Current is the default.
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
	psm := sm.New(stateInitial, 4, 4)

	p = &Poller{
		sm:     psm,
		intIn:  in,
		intOut: out,
		cmdCh:  make(chan sm.State, 1),
	}

	// POLL
	psm.On(cmdPoll, []sm.State{
		stateInitial,
		statePolling,
		statePaused,
	}, p.handlePoll)

	// PAUSE
	psm.On(cmdPause, []sm.State{
		statePolling,
	}, p.handlePause)

	// CONTINUE
	psm.On(cmdContinue, []sm.State{
		statePaused,
	}, p.handleContinue)

	// CLOSE
	psm.On(cmdClose, []sm.State{
		stateInitial,
		statePolling,
		statePaused,
	}, p.handleClose)

	return
}

// Poll -----------------------------------------------------------------------

type pollArgs struct {
	items  zmq.PollItems
	pollCh chan<- *PollResult
}

// Poll starts polling on the set of socket descriptors passed as a parameter.
// The channel passed into this method is used for sending notifications when
// gozmq.Poll() unblocks.
func (self *Poller) Poll(items zmq.PollItems, pollCh chan<- *PollResult) error {
	if len(items) == 0 {
		panic("Poll items are empty")
	}
	if pollCh == nil {
		panic("Poll channel is nil")
	}

	return self.sm.Emit(&sm.Event{
		cmdPoll,
		&pollArgs{items, pollCh},
	})
}

func (self *Poller) handlePoll(s sm.State, e *sm.Event) (next sm.State) {
	args := e.Data.(*pollArgs)

	self.items = args.items
	self.pollCh = args.pollCh

	// Break the current polling loop if there is any.
	if s == statePolling {
		err := self.interruptPolling(nil)
		if err != nil {
			args.pollCh <- &PollResult{-1, nil, err}
			close(self.pollCh)
		}
		self.cmdCh <- cmdPoll
	} else {
		go self.poll()
	}

	return statePolling
}

// Pause ----------------------------------------------------------------------

// Pause will pause polling until Continue is called again. This call breaks
// the call to gozmq.Poll and makes the poller wait for more commands to come.
//
// This method returns after gozmq.Poll is interrupted and 0MQ sockets are not
// being used any more.
func (self *Poller) Pause() error {
	errCh := make(chan error, 1)
	err := self.sm.Emit(&sm.Event{
		cmdPause,
		errCh,
	})
	if err != nil {
		return nil
	}
	return <-errCh
}

func (self *Poller) handlePause(s sm.State, e *sm.Event) (next sm.State) {
	errCh := e.Data.(chan<- error)
	errCh <- self.interruptPolling(nil)
	close(errCh)
	return statePaused
}

// Continue -------------------------------------------------------------------

// Continue resumes the poller after a call to Pause or after the polling is
// successful. The poller is automaticall paused before returning a PollResult,
// otherwise gozmq.Poll would keep returning again and again until the user
// reads what is available on the descriptors passed to the poller. We want to
// prevent such a busy-waiting and flooding of the result channel.
func (self *Poller) Continue() error {
	return self.sm.Emit(&sm.Event{
		cmdContinue,
		nil,
	})
}

func (self *Poller) handleContinue(s sm.State, e *sm.Event) (next sm.State) {
	self.cmdCh <- cmdContinue
	return statePolling
}

// Close ----------------------------------------------------------------------

// Close the poller and release all internal 0MQ sockets. This method blocks
// until all work is done.
func (self *Poller) Close() error {
	errCh := make(chan error, 1)
	err := self.sm.Emit(&sm.Event{
		cmdClose,
		errCh,
	})
	if err != nil {
		return err
	}
	return <-errCh
}

func (self *Poller) handleClose(s sm.State, e *sm.Event) (next sm.State) {
	closedCh := e.Data.(chan error)

	// Signal the internal goroutine to exit next time it gets blocked in select.
	close(self.cmdCh)

	// Break polling if necessary.
	// Use 'go' since we might not be blocked in Poll() and this call could
	// block the whole thing.
	if s == statePolling {
		go func() {
			if err := self.interruptPolling(nil); err != nil {
				closedCh <- err
				close(closedCh)
				closedCh = nil
			}
		}()
	}

	go func() {
		<-self.sm.TerminatedChannel()

		// Close internal 0MQ sockets.
		self.intIn.Close()
		self.intOut.Close()

		if self.pollCh != nil {
			close(self.pollCh)
			self.pollCh = nil
		}

		// Send notice to the user.
		if closedCh != nil {
			close(closedCh)
		}
	}()

	return stateClosed
}

// HELPERS --------------------------------------------------------------------

// Interrupt the call to gozmq.Poll().
func (self *Poller) interruptPolling(intCh chan struct{}) error {
	// Set up the callback interrupt channel.
	if intCh != nil {
		self.intCh = intCh
	} else {
		self.intCh = make(chan struct{})
	}

	// Interrupt gozmq.Poll()
	err := self.intIn.Send([]byte{0}, 0)
	if err != nil {
		close(self.intCh)
		self.intCh = nil
		return err
	}

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
	intItem := zmq.PollItem{
		Socket: self.intOut,
		Events: zmq.POLLIN,
	}

	items := append(self.items, intItem)

	for {
		// Poll on the poll items indefinitely.
		rc, err := zmq.Poll(items, -1)

		// Move to the PAUSED state.
		self.sm.SetState(statePaused)

		if err != nil {
			self.pollCh <- &PollResult{rc, nil, err}
			goto waitForOrders
		}

		if items[len(items)-1].REvents&zmq.POLLIN == 0 {
			// One of the user-defined sockets is available.
			self.pollCh <- &PollResult{rc, items[:len(items)-1], nil}
		} else {
			// The internal interrupt socket is available for reading.
			_, err = self.intOut.Recv(0)
			if err != nil {
				self.pollCh <- &PollResult{-1, nil, err}
			}

			// Signal that the polling was indeed interrupted.
			close(self.intCh)
		}

		// Wait for further commands.
	waitForOrders:
		cmd, ok := <-self.cmdCh
		if !ok {
			// The command channel has been closed, return.
			go self.sm.Terminate()
			return
		}
		switch cmd {
		case cmdPoll:
			items = append(self.items, intItem)
			fallthrough
		case cmdContinue:
			continue
		default:
			panic("Unexpected command received")
		}
	}
}

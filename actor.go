// Package phony is a small actor model library for Go, inspired by the causal messaging system in the Pony programming language.
// Messages should be non-blocking functions of 0 arguments.
// Message passing is causal: if A sends a message to C, and then later A sends a message to B that causes B to send a message to C, A's message to C will arrive before B's message to C.
// Message passing is asynchronous with unbounded queues, but with backpressure to pause an Actor that sends to a significantly more congested one.
package phony

import "sync"

// How large a queue can be before backpressure slows down sending to it.
const backpressureThreshold = 127

// An Actor maintans an inbox of messages and processes them 1 at a time.
// The intent is for the Actor struct to be embedded in other structs, where the other fields of the struct are only read or modified by the Actor.
// Messages are meant to be in the form of non-blocking closures.
// It is up to the user to ensure that memory is used safely, and that messages do not contain blocking operations.
// An Actor must not be copied after first use.
type Actor struct {
	mutex sync.Mutex
	queue []func()
}

// IActor is the interface for Actors, based on their ability to enqueue and send messages.
// It's meant so that structs which embed an Actor can be used with SendMessageTo and the like, rather than trying to depend on the concrete Actor type.
type IActor interface {
	Enqueue(func()) int
	SendMessageTo(IActor, func())
}

// Enqueue puts a message on the actor's queue and returns the new queue size.
// If you want to prevent flooding an actor faster than it can do work, then it's preferable to use SyncExec instead.
func (a *Actor) Enqueue(f func()) int {
	if f == nil {
		panic("tried to send nil message")
	}
	a.mutex.Lock()
	run := a.queue == nil
	a.queue = append(a.queue, f)
	l := len(a.queue)
	a.mutex.Unlock()
	if run {
		go a.run()
	}
	return l
}

// SendMessageTo should only be called on an actor by itself, and sends a message to another actor.
// Internally, it uses Enqueue and applies backpressure, so if the destination appears to be flooded then this Actor will (eventually) stop being schedled until the destination has gotten some work done.
func (a *Actor) SendMessageTo(destination IActor, message func()) {
	dLen := destination.Enqueue(message)
	if dLen > backpressureThreshold && destination != a {
		// Tried to send to someone else, with a large queue, so apply some backpressure
		// Sending backpressure to ourself is perfectly safe, but it's pointless extra work that only serves to slow things down even more, so we don't bother
		done := make(chan struct{})
		destination.Enqueue(func() { close(done) })
		a.Enqueue(func() { <-done })
	}
}

// SyncExec sends a message to an Actor and waits for it to be handled before returning.
// Actors should *not* use this to send messages to other Actors.
// It's meant to give outside goroutines a way to give work to Actors without flooding, and to inspect the internal state of structs that need to be accessed via an Actor.
func (a *Actor) SyncExec(f func()) {
	done := make(chan struct{})
	a.Enqueue(func() { f(); close(done) })
	<-done
}

func (a *Actor) run() {
	for {
		var f func()
		a.mutex.Lock()
		if len(a.queue) > 0 {
			f, a.queue = a.queue[0], a.queue[1:]
		} else {
			a.queue = nil
		}
		a.mutex.Unlock()
		if f != nil {
			f()
		} else {
			return
		}
	}
}

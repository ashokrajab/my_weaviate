//                           _       _
// __      _____  __ ___   ___  __ _| |_ ___
// \ \ /\ / / _ \/ _` \ \ / / |/ _` | __/ _ \
//  \ V  V /  __/ (_| |\ V /| | (_| | ||  __/
//   \_/\_/ \___|\__,_| \_/ |_|\__,_|\__\___|
//
//  Copyright © 2016 - 2023 Weaviate B.V. All rights reserved.
//
//  CONTACT: hello@weaviate.io
//

package cyclemanager

import (
	"context"
	"sync"

	"github.com/sirupsen/logrus"
)

type UnregisterFunc func(ctx context.Context) error

type callbacks interface {
	register(name string, cycleFunc CycleFunc) UnregisterFunc
	execute(shouldBreak ShouldBreakFunc) bool
}

type callbackState struct {
	name      string
	cycleFunc CycleFunc
	// indicates whether callback is already running - context active
	// or not running (already finished) - context expired
	// or not running (not yet started) - context nil
	runningCtx context.Context
}

type multiCallbacks struct {
	sync.Mutex

	logger  logrus.FieldLogger
	counter uint32
	states  map[uint32]*callbackState
	keys    []uint32
}

func newMultiCallbacks(logger logrus.FieldLogger) callbacks {
	return &multiCallbacks{
		logger:  logger,
		counter: 0,
		states:  map[uint32]*callbackState{},
		keys:    []uint32{},
	}
}

func (c *multiCallbacks) register(name string, cycleFunc CycleFunc) UnregisterFunc {
	c.Lock()
	defer c.Unlock()

	key := c.counter
	c.keys = append(c.keys, key)
	c.states[key] = &callbackState{
		name:       name,
		cycleFunc:  cycleFunc,
		runningCtx: nil,
	}
	c.counter++

	return func(ctx context.Context) error {
		return c.unregister(ctx, key)
	}
}

func (c *multiCallbacks) unregister(ctx context.Context, key uint32) error {
	for {
		// remove callback from collection only if not running (not yet started of finished)
		c.Lock()
		state, ok := c.states[key]
		if !ok {
			c.Unlock()
			return nil
		}
		runningCtx := state.runningCtx
		if runningCtx == nil || runningCtx.Err() != nil {
			delete(c.states, key)
			c.Unlock()
			return nil
		}
		c.Unlock()

		// wait for callback to finish
		select {
		case <-runningCtx.Done():
			// get back to the beginning of the loop to make sure state.runningCtx
			// was not changed. If not, loop will finish on runningCtx.Err() != nil check
			continue
		case <-ctx.Done():
			// in case both contexts are ready, but incoming ctx was selected
			// check again running ctx as priority one
			if runningCtx.Err() != nil {
				// get back to the beginning of the loop to make sure state.runningCtx
				// was not changed. If not, loop will finish on runningCtx.Err() != nil check
				continue
			}
			// incoming ctx expired
			return ctx.Err()
		}
	}
}

func (c *multiCallbacks) execute(shouldBreak ShouldBreakFunc) bool {
	executed := false
	i := 0
	for {
		if shouldBreak() {
			break
		}

		c.Lock()
		if i >= len(c.keys) {
			c.Unlock()
			break
		}

		key := c.keys[i]
		state, ok := c.states[key]
		if !ok { // callback deleted in the meantime, adjust keys and move to the next one
			c.keys = append(c.keys[:i], c.keys[i+1:]...)
			c.Unlock()
			continue
		}

		runningCtx, cancel := context.WithCancel(context.Background())
		state.runningCtx = runningCtx
		i++
		c.Unlock()

		func() {
			defer func() {
				if r := recover(); r != nil {
					c.logger.WithFields(logrus.Fields{
						"action":   "cyclemanager",
						"callback": state.name,
					}).Errorf("callback panic: %v", r)
				}
			}()
			executed = state.cycleFunc(shouldBreak) || executed
		}()
		cancel()
	}

	return executed
}

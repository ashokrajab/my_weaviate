//                           _       _
// __      _____  __ ___   ___  __ _| |_ ___
// \ \ /\ / / _ \/ _` \ \ / / |/ _` | __/ _ \
//  \ V  V /  __/ (_| |\ V /| | (_| | ||  __/
//   \_/\_/ \___|\__,_| \_/ |_|\__,_|\__\___|
//
//  Copyright © 2016 - 2021 SeMI Technologies B.V. All rights reserved.
//
//  CONTACT: hello@semi.technology
//

package lsmkv

import (
	"sync"

	"github.com/pkg/errors"
)

type Memtable struct {
	sync.RWMutex
	key                *binarySearchTree
	keyMulti           *binarySearchTreeMulti
	primaryIndex       *binarySearchTree
	commitlog          *commitLogger
	size               uint64
	path               string
	strategy           string
	secondaryIndices   uint16
	secondaryToPrimary []map[string][]byte
}

func newMemtable(path string, strategy string,
	secondaryIndices uint16) (*Memtable, error) {
	cl, err := newCommitLogger(path)
	if err != nil {
		return nil, errors.Wrap(err, "init commit logger")
	}

	m := &Memtable{
		key:              &binarySearchTree{},
		keyMulti:         &binarySearchTreeMulti{},
		primaryIndex:     &binarySearchTree{}, // todo, sort upfront
		commitlog:        cl,
		path:             path,
		strategy:         strategy,
		secondaryIndices: secondaryIndices,
	}

	if m.secondaryIndices > 0 {
		m.secondaryToPrimary = make([]map[string][]byte, m.secondaryIndices)
		for i := range m.secondaryToPrimary {
			m.secondaryToPrimary[i] = map[string][]byte{}
		}
	}

	return m, nil
}

type keyIndex struct {
	key           []byte
	secondaryKeys [][]byte
	valueStart    int
	valueEnd      int
}

func (l *Memtable) get(key []byte) ([]byte, error) {
	if l.strategy != StrategyReplace {
		return nil, errors.Errorf("get only possible with strategy 'replace'")
	}

	l.RLock()
	defer l.RUnlock()

	v, err := l.key.get(key)
	if err != nil {
		return nil, err
	}

	return v, nil
}

func (l *Memtable) getBySecondary(pos int, key []byte) ([]byte, error) {
	if l.strategy != StrategyReplace {
		return nil, errors.Errorf("get only possible with strategy 'replace'")
	}

	l.RLock()
	defer l.RUnlock()

	primary := l.secondaryToPrimary[pos][string(key)]
	if primary == nil {
		return nil, NotFound
	}

	v, err := l.key.get(primary)
	if err != nil {
		return nil, err
	}

	return v, nil
}

func (l *Memtable) put(key, value []byte, opts ...SecondaryKeyOption) error {
	if l.strategy != StrategyReplace {
		return errors.Errorf("put only possible with strategy 'replace'")
	}

	l.Lock()
	defer l.Unlock()
	// TODO: reflect secondary key in commit log
	if err := l.commitlog.put(key, value); err != nil {
		return errors.Wrap(err, "write into commit log")
	}

	var secondaryKeys [][]byte
	if l.secondaryIndices > 0 {
		secondaryKeys = make([][]byte, l.secondaryIndices)
		for _, opt := range opts {
			if err := opt(secondaryKeys); err != nil {
				return err
			}
		}
	}

	l.key.insert(key, value, secondaryKeys)
	l.size += uint64(len(key))
	l.size += uint64(len(value))

	for i, sec := range secondaryKeys {
		l.secondaryToPrimary[i][string(sec)] = key
	}

	return nil
}

func (l *Memtable) setTombstone(key []byte, opts ...SecondaryKeyOption) error {
	if l.strategy != "replace" {
		return errors.Errorf("setTombstone only possible with strategy 'replace'")
	}

	l.Lock()
	defer l.Unlock()

	if err := l.commitlog.setTombstone(key); err != nil {
		return errors.Wrap(err, "write into commit log")
	}

	var secondaryKeys [][]byte
	if l.secondaryIndices > 0 {
		secondaryKeys = make([][]byte, l.secondaryIndices)
		for _, opt := range opts {
			if err := opt(secondaryKeys); err != nil {
				return err
			}
		}
	}

	l.key.setTombstone(key, secondaryKeys)
	l.size += uint64(len(key)) + 1 // 1 byte for tombstone

	return nil
}

func (l *Memtable) getCollection(key []byte) ([]value, error) {
	if l.strategy != StrategySetCollection && l.strategy != StrategyMapCollection {
		return nil, errors.Errorf("getCollection only possible with strategies %q, %q",
			StrategySetCollection, StrategyMapCollection)
	}

	l.RLock()
	defer l.RUnlock()

	v, err := l.keyMulti.get(key)
	if err != nil {
		return nil, err
	}

	return v, nil
}

func (l *Memtable) append(key []byte, values []value) error {
	if l.strategy != StrategySetCollection && l.strategy != StrategyMapCollection {
		return errors.Errorf("append only possible with strategies %q, %q",
			StrategySetCollection, StrategyMapCollection)
	}

	l.Lock()
	defer l.Unlock()
	if err := l.commitlog.append(key, values); err != nil {
		return errors.Wrap(err, "write into commit log")
	}

	l.keyMulti.insert(key, values)
	l.size += uint64(len(key))
	for _, value := range values {
		l.size += uint64(len(value.value))
	}

	return nil
}

func (l *Memtable) Size() uint64 {
	l.RLock()
	defer l.RUnlock()

	return l.size
}

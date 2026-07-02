package jsn

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

const (
	nChildrenLog2 = 4
	nChildren     = 1 << nChildrenLog2
	nChildrenMask = nChildren - 1
)

type hashTrieMap struct {
	root atomic.Pointer[indirectNode]
	mu   sync.Mutex
}

type trieNode struct {
	isEntry bool
}

type indirectNode struct {
	trieNode
	children [nChildren]atomic.Pointer[trieNode]
}

type entryNode struct {
	trieNode
	overflow atomic.Pointer[entryNode]
	key      uintptr
	value    *cachedEncoder
}

func hashKey(k uintptr) uint64 {
	return uint64(k>>3) * 0x9E3779B97F4A7C15
}

func (m *hashTrieMap) Load(key uintptr) (*cachedEncoder, bool) {
	i := m.root.Load()
	if i == nil {
		return nil, false
	}

	hash := hashKey(key)
	hashShift := uint(8 * unsafe.Sizeof(uintptr(0)))

	for hashShift != 0 {
		hashShift -= nChildrenLog2

		n := i.children[(hash>>hashShift)&nChildrenMask].Load()
		if n == nil {
			return nil, false
		}

		if n.isEntry {
			return n.entry().lookup(key)
		}

		i = n.indirect()
	}

	panic("jsn: hashtrie ran out of hash bits")
}

func (m *hashTrieMap) LoadOrStore(key uintptr, value *cachedEncoder) (*cachedEncoder, bool) {
	if v, ok := m.Load(key); ok {
		return v, true
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.root.Load() == nil {
		m.root.Store(&indirectNode{})
	}

	hash := hashKey(key)
	i := m.root.Load()
	hashShift := uint(8 * unsafe.Sizeof(uintptr(0)))

	var (
		slot *atomic.Pointer[trieNode]
		n    *trieNode
	)

	for hashShift != 0 {
		hashShift -= nChildrenLog2

		slot = &i.children[(hash>>hashShift)&nChildrenMask]
		n = slot.Load()
		if n == nil {
			break
		}

		if n.isEntry {
			if v, ok := n.entry().lookup(key); ok {
				return v, true
			}

			break
		}

		i = n.indirect()
	}

	if hashShift == 0 {
		panic("jsn: hashtrie ran out of hash bits")
	}

	newEntry := &entryNode{trieNode: trieNode{isEntry: true}, key: key, value: value}

	if n == nil {
		slot.Store(&newEntry.trieNode)
	} else {
		oldEntry := n.entry()

		slot.Store(m.expand(oldEntry, newEntry, hash, hashShift))
	}

	return value, false
}

func (m *hashTrieMap) expand(oldEntry, newEntry *entryNode, newHash uint64, hashShift uint) *trieNode {
	oldHash := hashKey(oldEntry.key)

	if oldHash == newHash {
		newEntry.overflow.Store(oldEntry)

		return &newEntry.trieNode
	}

	top := &indirectNode{}
	current := top

	for {
		if hashShift == 0 {
			panic("jsn: hashtrie ran out of hash bits during expand")
		}

		hashShift -= nChildrenLog2

		oi := (oldHash >> hashShift) & nChildrenMask
		ni := (newHash >> hashShift) & nChildrenMask

		if oi != ni {
			current.children[oi].Store(&oldEntry.trieNode)
			current.children[ni].Store(&newEntry.trieNode)

			break
		}

		next := &indirectNode{}
		current.children[oi].Store(&next.trieNode)
		current = next
	}

	return &top.trieNode
}

func (n *trieNode) entry() *entryNode {
	return (*entryNode)(unsafe.Pointer(n))
}

func (n *trieNode) indirect() *indirectNode {
	return (*indirectNode)(unsafe.Pointer(n))
}

func (e *entryNode) lookup(key uintptr) (*cachedEncoder, bool) {
	for e != nil {
		if e.key == key {
			return e.value, true
		}

		e = e.overflow.Load()
	}

	return nil, false
}

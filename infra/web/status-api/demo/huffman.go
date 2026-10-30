package demo

// Port of the adaptive Huffman coder in iortcw qcommon/huffman.c.  Demo
// messages are decoded with a tree that MSG_initHuffman pre-trains from
// msgHData and then never updates, so only tree construction (addRef) and
// symbol lookup are needed here.

const (
	hmax         = 256
	nyt          = hmax
	internalNode = hmax + 1
)

type hnode struct {
	left, right, parent *hnode
	next, prev          *hnode
	head                **hnode
	weight              int
	symbol              int
}

type huff struct {
	tree  *hnode
	lhead *hnode
	loc   [hmax + 1]*hnode
}

func newHuff() *huff {
	h := &huff{}
	n := &hnode{symbol: nyt}
	h.tree, h.lhead, h.loc[nyt] = n, n, n
	return h
}

// The C code recycles head pointers through a freelist; that is only a
// memory optimisation, so a fresh allocation is equivalent.
func newPPNode() **hnode { return new(*hnode) }

func swapNodes(h *huff, n1, n2 *hnode) {
	par1, par2 := n1.parent, n2.parent
	if par1 != nil {
		if par1.left == n1 {
			par1.left = n2
		} else {
			par1.right = n2
		}
	} else {
		h.tree = n2
	}
	if par2 != nil {
		if par2.left == n2 {
			par2.left = n1
		} else {
			par2.right = n1
		}
	} else {
		h.tree = n1
	}
	n1.parent = par2
	n2.parent = par1
}

func swapList(n1, n2 *hnode) {
	par1 := n1.next
	n1.next = n2.next
	n2.next = par1

	par1 = n1.prev
	n1.prev = n2.prev
	n2.prev = par1

	if n1.next == n1 {
		n1.next = n2
	}
	if n2.next == n2 {
		n2.next = n1
	}
	if n1.next != nil {
		n1.next.prev = n1
	}
	if n2.next != nil {
		n2.next.prev = n2
	}
	if n1.prev != nil {
		n1.prev.next = n1
	}
	if n2.prev != nil {
		n2.prev.next = n2
	}
}

func increment(h *huff, node *hnode) {
	if node == nil {
		return
	}
	if node.next != nil && node.next.weight == node.weight {
		lnode := *node.head
		if lnode != node.parent {
			swapNodes(h, lnode, node)
		}
		swapList(lnode, node)
	}
	if node.prev != nil && node.prev.weight == node.weight {
		*node.head = node.prev
	} else {
		*node.head = nil
	}
	node.weight++
	if node.next != nil && node.next.weight == node.weight {
		node.head = node.next.head
	} else {
		node.head = newPPNode()
		*node.head = node
	}
	if node.parent != nil {
		increment(h, node.parent)
		if node.prev == node.parent {
			swapList(node, node.parent)
			if *node.head == node {
				*node.head = node.parent
			}
		}
	}
}

func (h *huff) addRef(ch byte) {
	if h.loc[ch] != nil {
		increment(h, h.loc[ch])
		return
	}
	// first transmission of this symbol
	tnode := &hnode{}
	tnode2 := &hnode{}

	tnode2.symbol = internalNode
	tnode2.weight = 1
	tnode2.next = h.lhead.next
	if h.lhead.next != nil {
		h.lhead.next.prev = tnode2
		if h.lhead.next.weight == 1 {
			tnode2.head = h.lhead.next.head
		} else {
			tnode2.head = newPPNode()
			*tnode2.head = tnode2
		}
	} else {
		tnode2.head = newPPNode()
		*tnode2.head = tnode2
	}
	h.lhead.next = tnode2
	tnode2.prev = h.lhead

	tnode.symbol = int(ch)
	tnode.weight = 1
	tnode.next = h.lhead.next
	if h.lhead.next != nil {
		h.lhead.next.prev = tnode
		if h.lhead.next.weight == 1 {
			tnode.head = h.lhead.next.head
		} else {
			// "this should never happen" in the C source; kept verbatim
			tnode.head = newPPNode()
			*tnode.head = tnode2
		}
	} else {
		tnode.head = newPPNode()
		*tnode.head = tnode
	}
	h.lhead.next = tnode
	tnode.prev = h.lhead
	tnode.left, tnode.right = nil, nil

	if h.lhead.parent != nil {
		if h.lhead.parent.left == h.lhead {
			h.lhead.parent.left = tnode2
		} else {
			h.lhead.parent.right = tnode2
		}
	} else {
		h.tree = tnode2
	}

	tnode2.right = tnode
	tnode2.left = h.lhead
	tnode2.parent = h.lhead.parent
	h.lhead.parent = tnode2
	tnode.parent = tnode2

	h.loc[ch] = tnode
	increment(h, tnode2.parent)
}

// msgTree is built once: the same tree is used to decode every message.
var msgTree = func() *hnode {
	h := newHuff()
	for i := 0; i < 256; i++ {
		for j := 0; j < msgHData[i]; j++ {
			h.addRef(byte(i))
		}
	}
	return h.tree
}()

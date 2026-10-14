// Package otbnode reads and writes the escaped node tree used by items.otb
// and OTBM maps: a 4-byte identifier followed by nodes. A node starts with
// 0xFE and its type byte, holds property bytes and child nodes, and ends
// with 0xFF. Property bytes equal to 0xFD, 0xFE or 0xFF are escaped with 0xFD.
package otbnode

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

const (
	nodeStart = 0xFE
	nodeEnd   = 0xFF
	escape    = 0xFD
)

// ErrFormat is returned for malformed node files.
var ErrFormat = errors.New("otbnode: malformed node file")

// Node is one parsed node. Props holds the unescaped property bytes
// (without the type byte).
type Node struct {
	Type     byte
	Props    []byte
	Children []*Node
}

// Parse reads a node file. ident receives the 4-byte identifier.
func Parse(data []byte) (root *Node, ident [4]byte, err error) {
	return ParseProgress(data, nil)
}

// progressStep is how many bytes are parsed between progress calls.
const progressStep = 1 << 20

// ParseProgress is Parse that calls progress (when not nil) with the bytes
// parsed so far and the total.
func ParseProgress(data []byte, progress func(done, total int)) (root *Node, ident [4]byte, err error) {
	return parse(data, &parser{buf: make([]byte, 0, len(data)), progress: progress})
}

// Stream parses a node file without keeping all of it: every node at depth
// (the root is depth 0) is handed to visit with its finished subtree and
// then dropped, so only one such subtree is in memory at a time. stack holds
// the nodes above it, with their properties (their Children lack the
// streamed nodes). Nodes above depth are returned in root as usual.
// Properties and nodes passed to visit are only valid during the call.
func Stream(data []byte, depth int, progress func(done, total int), visit func(stack []*Node, n *Node) error) (root *Node, ident [4]byte, err error) {
	return parse(data, &parser{progress: progress, depth: depth, visit: visit})
}

func parse(data []byte, p *parser) (root *Node, ident [4]byte, err error) {
	if len(data) < 6 {
		return nil, ident, fmt.Errorf("%w: file too small", ErrFormat)
	}
	copy(ident[:], data[:4])
	p.data, p.pos, p.next = data, 4, progressStep
	if p.data[p.pos] != nodeStart {
		return nil, ident, fmt.Errorf("%w: no root node", ErrFormat)
	}
	p.pos++
	root, err = p.node()
	return root, ident, err
}

type parser struct {
	data []byte
	pos  int
	// buf holds every node's unescaped properties back to back; nodes slice
	// into it, so parsing allocates one buffer instead of one per node.
	buf      []byte
	progress func(done, total int)
	next     int
	// Streaming (see Stream): nodes at depth go to visit, stack is the path.
	depth int
	visit func(stack []*Node, n *Node) error
	stack []*Node
}

// node parses a node whose start marker was consumed.
func (p *parser) node() (*Node, error) {
	if p.pos >= len(p.data) {
		return nil, fmt.Errorf("%w: truncated node", ErrFormat)
	}
	n := &Node{Type: p.data[p.pos]}
	p.pos++
	start := len(p.buf)
	streamKids := p.visit != nil && len(p.stack)+1 == p.depth
	if p.visit != nil {
		p.stack = append(p.stack, n)
		defer func() { p.stack = p.stack[:len(p.stack)-1] }()
	}
	kids := 0
	for p.pos < len(p.data) {
		b := p.data[p.pos]
		switch b {
		case nodeStart:
			if kids == 0 {
				// Copied: streamed children reuse the buffer after it.
				n.Props = p.buf[start:len(p.buf):len(p.buf)]
				if streamKids {
					n.Props = append([]byte(nil), n.Props...)
				}
			}
			kids++
			p.pos++
			mark := len(p.buf)
			child, err := p.node()
			if err != nil {
				return nil, err
			}
			if streamKids {
				if err := p.visit(p.stack, child); err != nil {
					return nil, err
				}
				p.buf = p.buf[:mark]
			} else {
				n.Children = append(n.Children, child)
			}
			// Properties after children are not part of the format.
			start = len(p.buf)
		case nodeEnd:
			if kids == 0 {
				n.Props = p.buf[start:len(p.buf):len(p.buf)]
			}
			p.pos++
			return n, nil
		case escape:
			if p.pos+1 >= len(p.data) {
				return nil, fmt.Errorf("%w: dangling escape", ErrFormat)
			}
			p.buf = append(p.buf, p.data[p.pos+1])
			p.pos += 2
		default:
			// Copy the run of plain bytes at once.
			end := p.pos + 1
			for end < len(p.data) {
				c := p.data[end]
				if c == nodeStart || c == nodeEnd || c == escape {
					break
				}
				end++
			}
			p.buf = append(p.buf, p.data[p.pos:end]...)
			p.pos = end
			if p.progress != nil && p.pos >= p.next {
				p.progress(p.pos, len(p.data))
				p.next = p.pos + progressStep
			}
		}
	}
	return nil, fmt.Errorf("%w: missing node end", ErrFormat)
}

// Reader reads little-endian values from node properties with a sticky
// error.
type Reader struct {
	b   []byte
	pos int
	err error
}

// NewReader returns a reader over props.
func NewReader(props []byte) *Reader { return &Reader{b: props} }

// Err returns the first read error.
func (r *Reader) Err() error { return r.err }

// Remaining is the number of unread bytes.
func (r *Reader) Remaining() int { return len(r.b) - r.pos }

// More reports whether bytes are left and no error happened.
func (r *Reader) More() bool { return r.err == nil && r.pos < len(r.b) }

func (r *Reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || r.pos+n > len(r.b) {
		r.err = fmt.Errorf("%w: property needs %d bytes, %d left", ErrFormat, n, len(r.b)-r.pos)
		return nil
	}
	s := r.b[r.pos : r.pos+n]
	r.pos += n
	return s
}

// U8 reads a byte.
func (r *Reader) U8() uint8 {
	if b := r.take(1); b != nil {
		return b[0]
	}
	return 0
}

// U16 reads a little-endian uint16.
func (r *Reader) U16() uint16 {
	if b := r.take(2); b != nil {
		return binary.LittleEndian.Uint16(b)
	}
	return 0
}

// U32 reads a little-endian uint32.
func (r *Reader) U32() uint32 {
	if b := r.take(4); b != nil {
		return binary.LittleEndian.Uint32(b)
	}
	return 0
}

// U64 reads a little-endian uint64.
func (r *Reader) U64() uint64 {
	if b := r.take(8); b != nil {
		return binary.LittleEndian.Uint64(b)
	}
	return 0
}

// Bytes reads n raw bytes.
func (r *Reader) Bytes(n int) []byte { return r.take(n) }

// Skip skips n bytes.
func (r *Reader) Skip(n int) { r.take(n) }

// String reads a string with a uint16 length.
func (r *Reader) String() string {
	n := int(r.U16())
	return string(r.take(n))
}

// LongString reads a string with a uint32 length.
func (r *Reader) LongString() string {
	n := r.U32()
	if n > math.MaxInt32 {
		r.err = fmt.Errorf("%w: string too long", ErrFormat)
		return ""
	}
	return string(r.take(int(n)))
}

// Writer writes a node file with escaping.
type Writer struct {
	w   *bufio.Writer
	err error
	tmp [8]byte
}

// NewWriter writes the identifier and returns a writer.
func NewWriter(w io.Writer, ident [4]byte) *Writer {
	bw := bufio.NewWriterSize(w, 1<<20)
	_, err := bw.Write(ident[:])
	return &Writer{w: bw, err: err}
}

// Start opens a node of the given type.
func (w *Writer) Start(t byte) {
	w.raw(nodeStart)
	w.U8(t)
}

// End closes the current node.
func (w *Writer) End() { w.raw(nodeEnd) }

func (w *Writer) raw(b byte) {
	if w.err == nil {
		w.err = w.w.WriteByte(b)
	}
}

// U8 writes an escaped byte.
func (w *Writer) U8(b byte) {
	if b == nodeStart || b == nodeEnd || b == escape {
		w.raw(escape)
	}
	w.raw(b)
}

// Bytes writes escaped bytes.
func (w *Writer) Bytes(p []byte) {
	for _, b := range p {
		w.U8(b)
	}
}

// U16 writes a little-endian uint16.
func (w *Writer) U16(v uint16) {
	binary.LittleEndian.PutUint16(w.tmp[:], v)
	w.Bytes(w.tmp[:2])
}

// U32 writes a little-endian uint32.
func (w *Writer) U32(v uint32) {
	binary.LittleEndian.PutUint32(w.tmp[:], v)
	w.Bytes(w.tmp[:4])
}

// U64 writes a little-endian uint64.
func (w *Writer) U64(v uint64) {
	binary.LittleEndian.PutUint64(w.tmp[:], v)
	w.Bytes(w.tmp[:8])
}

// String writes a string with a uint16 length (truncated to 65535 bytes).
func (w *Writer) String(s string) {
	if len(s) > math.MaxUint16 {
		s = s[:math.MaxUint16]
	}
	w.U16(uint16(len(s)))
	w.Bytes([]byte(s))
}

// LongString writes a string with a uint32 length.
func (w *Writer) LongString(s string) {
	w.U32(uint32(len(s)))
	w.Bytes([]byte(s))
}

// Flush writes buffered data and returns the first error.
func (w *Writer) Flush() error {
	if w.err != nil {
		return w.err
	}
	return w.w.Flush()
}

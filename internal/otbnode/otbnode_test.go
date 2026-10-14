package otbnode

import (
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, [4]byte{'O', 'T', 'B', 'M'})
	w.Start(1)
	w.U32(0xFEFDFF00)
	w.String("a\xfeb")
	w.Start(2)
	w.U16(0xFFFE)
	w.End()
	w.Start(3)
	w.LongString("long")
	w.Start(4)
	w.End()
	w.End()
	w.End()
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	root, ident, err := Parse(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if string(ident[:]) != "OTBM" || root.Type != 1 || len(root.Children) != 2 {
		t.Fatalf("bad root: %q %d %d", ident, root.Type, len(root.Children))
	}
	r := NewReader(root.Props)
	if v := r.U32(); v != 0xFEFDFF00 {
		t.Fatalf("u32 = %x", v)
	}
	if s := r.String(); s != "a\xfeb" {
		t.Fatalf("string = %q", s)
	}
	if r.More() || r.Err() != nil {
		t.Fatal("unexpected trailing data")
	}
	if v := NewReader(root.Children[0].Props).U16(); v != 0xFFFE {
		t.Fatalf("child u16 = %x", v)
	}
	c := root.Children[1]
	if s := NewReader(c.Props).LongString(); s != "long" || len(c.Children) != 1 || c.Children[0].Type != 4 {
		t.Fatalf("bad second child %q", s)
	}
}

func TestMalformed(t *testing.T) {
	for _, data := range [][]byte{
		{0, 0, 0, 0, 0xFE, 1, 2},          // no end
		{0, 0, 0, 0, 1, 1, 0xFF},          // no start
		{0, 0, 0, 0, 0xFE, 1, 0xFD},       // dangling escape
		{0, 0, 0, 0, 0xFE, 1, 0xFE, 2, 3}, // unterminated child
	} {
		if _, _, err := Parse(data); err == nil {
			t.Errorf("Parse(%x) succeeded", data)
		}
	}
}

func TestReaderErrors(t *testing.T) {
	r := NewReader([]byte{1})
	r.U16()
	if r.Err() == nil {
		t.Fatal("short read did not fail")
	}
	if r.U8() != 0 {
		t.Fatal("read after error returned data")
	}
}

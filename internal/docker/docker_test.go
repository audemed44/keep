package docker

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func frame(stream byte, s string) []byte {
	hdr := make([]byte, 8)
	hdr[0] = stream
	binary.BigEndian.PutUint32(hdr[4:], uint32(len(s)))
	return append(hdr, s...)
}

func TestDemux(t *testing.T) {
	var in bytes.Buffer
	in.Write(frame(1, "hello "))
	in.Write(frame(2, "oops"))
	in.Write(frame(1, "world"))
	var out, errOut bytes.Buffer
	if err := demux(&in, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello world" || errOut.String() != "oops" {
		t.Fatalf("stdout %q, stderr %q", out.String(), errOut.String())
	}

	if err := demux(bytes.NewReader(frame(1, "cut")[:9]), &out, &errOut); err == nil {
		t.Fatal("a truncated frame passed")
	}
}

func TestTail(t *testing.T) {
	tl := &tail{max: 5}
	tl.Write([]byte("abc"))
	tl.Write([]byte("defgh"))
	if tl.String() != "defgh" {
		t.Fatalf("got %q", tl.String())
	}
	e := &ExitError{Code: 2, Stderr: " bad \n"}
	if !strings.Contains(e.Error(), "exit status 2: bad") {
		t.Fatal(e.Error())
	}
}

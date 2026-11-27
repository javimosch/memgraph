package cloudserver

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
)

// prescan limits what a push may unpack to. git's receive.maxInputSize limits
// only the compressed bytes.
func (s *Server) prescan(f *os.File, size int64) error {
	// Skip the pkt-line command list, which ends with a flush packet.
	var off int64
	for {
		var h [4]byte
		if _, err := f.ReadAt(h[:], off); err != nil {
			return errors.New("bad command list")
		}
		n, err := strconv.ParseUint(string(h[:]), 16, 32)
		if err != nil || (n > 0 && n < 4) {
			return errors.New("bad command list")
		}
		if n == 0 {
			off += 4
			break
		}
		off += int64(n)
	}
	if off >= size {
		return nil // no pack, e.g. git's probe request
	}
	return s.checkPack(io.NewSectionReader(f, off, size-off))
}

func (s *Server) checkPack(r io.ReadSeeker) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("pack parser panic: %v", p)
		}
	}()
	sc := packfile.NewScanner(r)
	_, count, err := sc.Header()
	if err != nil {
		return err
	}
	if int64(count) > int64(s.lim.Objects) {
		return errors.New("too many objects")
	}
	var total int64
	for i := uint32(0); i < count; i++ {
		h, err := sc.NextObjectHeader()
		if err != nil {
			return err
		}
		if h.Length > s.lim.Object {
			return errors.New("object too large")
		}
		lw := &limitWriter{max: s.lim.Object}
		if _, _, err := sc.NextObject(lw); err != nil {
			return err
		}
		n := lw.n
		if h.Type == plumbing.OFSDeltaObject || h.Type == plumbing.REFDeltaObject {
			// The delta header declares the result size, whatever the base is.
			target, err := deltaTarget(lw.head)
			if err != nil {
				return err
			}
			if target > uint64(s.lim.Object) {
				return errors.New("object too large")
			}
			n += int64(target)
		}
		if total += n; total > s.lim.Total {
			return errors.New("push unpacks too large")
		}
	}
	return nil
}

type limitWriter struct {
	n, max int64
	head   []byte
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n += int64(len(p)); l.n > l.max {
		return 0, errors.New("object too large")
	}
	if need := 32 - len(l.head); need > 0 {
		l.head = append(l.head, p[:min(need, len(p))]...)
	}
	return len(p), nil
}

func deltaTarget(b []byte) (uint64, error) {
	var vals [2]uint64
	for k := range vals {
		for shift := 0; ; shift += 7 {
			if len(b) == 0 || shift > 56 {
				return 0, errors.New("bad delta header")
			}
			c := b[0]
			b = b[1:]
			vals[k] |= uint64(c&0x7f) << shift
			if c&0x80 == 0 {
				break
			}
		}
	}
	return vals[1], nil
}

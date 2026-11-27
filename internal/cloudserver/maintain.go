package cloudserver

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/javimosch/memgraph/internal/fsx"
)

// pushCountFile, inside a repository, counts pushes since the last successful
// maintenance, so a restart does not forget that it is due.
const pushCountFile = "memgraph-pushes"

// retryAfterPushes is how soon failed maintenance is tried again.
const retryAfterPushes = 10

type project struct {
	pushes sync.RWMutex // pushes share it; maintenance takes it exclusively
	mu     sync.Mutex   // guards count and repack
	count  int
	repack bool
}

func (s *Server) project(name string) *project {
	if p, ok := s.projects.Load(name); ok {
		return p.(*project)
	}
	fresh := &project{}
	if raw, err := os.ReadFile(filepath.Join(s.data.repo(name), pushCountFile)); err == nil {
		fresh.count, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
	}
	p, _ := s.projects.LoadOrStore(name, fresh)
	return p.(*project)
}

// saveCount stores the counter. The caller holds p.mu.
func (s *Server) saveCount(name string, p *project) {
	path := filepath.Join(s.data.repo(name), pushCountFile)
	if err := fsx.WriteFileAtomic(path, []byte(strconv.Itoa(p.count)+"\n"), 0600); err != nil {
		log.Printf("%s: cannot save the push count: %v", name, err)
	}
}

func (s *Server) afterPush(name string, p *project) {
	if s.repackEvery <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.count++
	s.saveCount(name, p)
	s.maintainIfDue(name, p)
}

// maintainIfDue starts maintenance in the background. Pushes wait for it,
// fetches do not. The caller holds p.mu.
func (s *Server) maintainIfDue(name string, p *project) {
	if s.repackEvery <= 0 || p.count < s.repackEvery || p.repack {
		return
	}
	p.repack = true
	s.maintenance.Add(1)
	go func() {
		defer s.maintenance.Done()
		p.pushes.Lock() // waits for pushes in progress
		defer p.pushes.Unlock()
		start := time.Now()
		out, err := s.maintain(name)
		log.Printf("repack %s: %v err=%v %s", name, time.Since(start), err, out)
		p.mu.Lock()
		defer p.mu.Unlock()
		p.repack = false
		if err == nil {
			p.count = 0
		} else {
			p.count = max(0, s.repackEvery-retryAfterPushes)
		}
		s.saveCount(name, p)
	}()
}

// maintain compacts a repository. A push that loses the ref race has already
// left its objects behind, so it prunes too. Both are safe only while no push
// runs, and a hung git would hold pushes back forever, hence the time limit.
func (s *Server) maintain(name string) (out []byte, err error) {
	for _, args := range [][]string{{"repack", "-adq"}, {"prune", "--expire=now"}} {
		full := append([]string{"--signal=KILL", strconv.Itoa(s.lim.MaintenanceSeconds),
			s.git, "-C", s.data.repo(name)}, args...)
		cmd := exec.Command(s.timeout, full...)
		cmd.Env = s.env
		if out, err = cmd.CombinedOutput(); err != nil {
			break
		}
	}
	return out, err
}

// resumeMaintenance runs what was due when the server last stopped.
func (s *Server) resumeMaintenance() error {
	names, err := s.data.Projects()
	if err != nil {
		return err
	}
	for _, name := range names {
		p := s.project(name)
		p.mu.Lock()
		s.maintainIfDue(name, p)
		p.mu.Unlock()
	}
	return nil
}

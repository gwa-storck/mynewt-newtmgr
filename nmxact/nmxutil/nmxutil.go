package nmxutil

import (
	"math/rand"
	"os"
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
)

var nextNmpSeq uint8
var beenRead bool
var seqMutex sync.Mutex

var ListenLog = &log.Logger{
	Out:       os.Stderr,
	Formatter: &log.TextFormatter{ForceColors: true},
	Level:     log.DebugLevel,
}

func SetLogLevel(level log.Level) {
	log.SetLevel(level)
	log.SetFormatter(&log.TextFormatter{ForceColors: true})
	ListenLog.Level = level
}

func NextNmpSeq() uint8 {
	seqMutex.Lock()
	defer seqMutex.Unlock()

	if !beenRead {
		nextNmpSeq = uint8(rand.Uint32())
		beenRead = true
	}

	val := nextNmpSeq
	nextNmpSeq++

	return val
}

type SingleResource struct {
	acquired  bool
	waitQueue [](chan error)
	mtx       sync.Mutex
}

func NewSingleResource() SingleResource {
	return SingleResource{
		waitQueue: [](chan error){},
	}
}

func (s *SingleResource) Acquire() error {
	s.mtx.Lock()

	if !s.acquired {
		s.acquired = true
		s.mtx.Unlock()
		return nil
	}

	w := make(chan error)
	s.waitQueue = append(s.waitQueue, w)

	s.mtx.Unlock()

	err := <-w
	if err != nil {
		return err
	}

	return nil
}

func (s *SingleResource) Release() {
	s.mtx.Lock()

	if !s.acquired {
		panic("SingleResource release without acquire")
		s.mtx.Unlock()
		return
	}

	if len(s.waitQueue) == 0 {
		s.acquired = false
		s.mtx.Unlock()
		return
	}

	w := s.waitQueue[0]
	s.waitQueue = s.waitQueue[1:]

	s.mtx.Unlock()

	w <- nil
}

func (s *SingleResource) Abort(err error) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	for _, w := range s.waitQueue {
		w <- err
	}
	s.waitQueue = [](chan error){}
}

type ErrLessFn func(a error, b error) bool
type ErrProcFn func(err error)

// Aggregates errors that occur close in time.  The most severe error gets
// reported.
type ErrFunnel struct {
	LessCb     ErrLessFn
	ProcCb     ErrProcFn
	AccumDelay time.Duration

	mtx      sync.Mutex
	resetMtx sync.Mutex
	curErr   error
	errTimer *time.Timer
	started  bool
}

func (f *ErrFunnel) Start() {
	f.resetMtx.Lock()

	f.mtx.Lock()
	defer f.mtx.Unlock()

	f.started = true
}

func (f *ErrFunnel) Insert(err error) {
	if err == nil {
		panic("ErrFunnel nil insert")
	}

	f.mtx.Lock()
	defer f.mtx.Unlock()

	if !f.started {
		panic("ErrFunnel insert without start")
	}

	if f.curErr == nil {
		f.curErr = err
		f.errTimer = time.AfterFunc(f.AccumDelay, func() {
			f.timerExp()
		})
	} else {
		if f.LessCb(f.curErr, err) {
			if !f.errTimer.Stop() {
				<-f.errTimer.C
			}
			f.curErr = err
			f.errTimer.Reset(f.AccumDelay)
		}
	}
}

func (f *ErrFunnel) Reset() {
	f.mtx.Lock()
	defer f.mtx.Unlock()

	if f.started {
		f.started = false
		f.curErr = nil
		f.errTimer.Stop()
		f.resetMtx.Unlock()
	}
}

func (f *ErrFunnel) BlockUntilExp() {
	f.mtx.Lock()
	defer f.mtx.Unlock()

	if f.started {
		f.resetMtx.Lock()
		f.resetMtx.Unlock()
	}
}

func (f *ErrFunnel) timerExp() {
	f.mtx.Lock()
	err := f.curErr
	f.curErr = nil
	f.mtx.Unlock()

	if err == nil {
		panic("ErrFunnel timer expired but no error")
	}

	f.ProcCb(err)
}

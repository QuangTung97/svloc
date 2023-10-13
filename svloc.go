package svloc

import (
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
)

// Universe every universe is different
// each newFn in a Locator[T] will be called once for each Universe
// but different Universes can have different values of object created by values
// The order of calls MUST be:
//
//	Register in global => Wrap / Override / MustOverride => Get
//
// It will return errors or panic (with Must* functions) if Get happens before any of those functions
type Universe struct {
	data       *universeData
	currentKey any
	prev       *Universe // linked list of Universe
}

// universeData is the real global structure
// Universe just a local context object
type universeData struct {
	mut    sync.Mutex
	svcMap map[any]*registeredService
}

// NewUniverse creates a new Universe
func NewUniverse() *Universe {
	return &Universe{
		data: &universeData{
			svcMap: map[any]*registeredService{},
		},
		currentKey: nil,
		prev:       nil,
	}
}

type registeredService struct {
	key any

	// ==========================================
	onceDone        atomic.Bool // similar to sync.Once, but add a mechanism for detecting deadlock
	onceMut         sync.Mutex
	svc             any
	getCallLocation string
	// ==========================================

	// ================================================
	funcMut  sync.Mutex
	newFunc  func(unv *Universe) any
	wrappers []func(svc any) any
	// ================================================
}

func (s *registeredService) newService(unv *Universe, callLoc string) any {
	if s.onceDone.Load() {
		return s.svc
	}
	return s.newServiceSlow(unv, callLoc)
}

func (s *registeredService) newServiceSlow(unv *Universe, callLoc string) any {
	for {
		ok := s.onceMut.TryLock()
		if ok {
			break
		}
		if unv.detectCircularDependency(s.key) {
			panic("svloc: circular dependency detected")
		}
	}
	defer s.onceMut.Unlock()

	// double-checked locking
	if s.onceDone.Load() {
		return s.svc
	}

	s.svc = s.newFunc(&Universe{
		data:       unv.data,
		currentKey: s.key,
		prev:       unv,
	})
	for _, wrapper := range s.wrappers {
		s.svc = wrapper(s.svc)
	}

	s.getCallLocation = callLoc

	s.onceDone.Store(true)

	return s.svc
}

func (u *Universe) detectCircularDependency(newKey any) bool {
	for currentUnv := u; currentUnv != nil; currentUnv = currentUnv.prev {
		if currentUnv.currentKey == newKey {
			return true
		}
	}
	return false
}

func (u *universeData) getService(key any) *registeredService {
	u.mut.Lock()
	defer u.mut.Unlock()

	svc, existed := u.svcMap[key]
	if existed {
		return svc
	}

	svc = &registeredService{
		key: key,
	}
	u.svcMap[key] = svc

	return svc
}

// Locator ...
type Locator[T any] struct {
	key   *T
	newFn func(unv *Universe) T
}

// Get ...
func (s *Locator[T]) Get(unv *Universe) T {
	reg := unv.data.getService(s.key)

	reg.funcMut.Lock()
	if reg.newFunc == nil {
		reg.newFunc = func(unv *Universe) any {
			return s.newFn(unv)
		}
	}
	reg.funcMut.Unlock()

	_, file, line, _ := runtime.Caller(1)
	loc := fmt.Sprintf("%s:%d", file, line)

	svc := reg.newService(unv, loc)
	return svc.(T)
}

// Override prevents running the function inside Register
func (s *Locator[T]) Override(unv *Universe, svc T) error {
	return s.OverrideFunc(unv, func(unv *Universe) T {
		return svc
	})
}

func (s *Locator[T]) doBeforeGet(unv *Universe, handler func(reg *registeredService)) error {
	reg := unv.data.getService(s.key)

	reg.funcMut.Lock()
	defer reg.funcMut.Unlock()

	if reg.onceDone.Load() {
		fmt.Printf("[ERROR] Get called location: %s\n", reg.getCallLocation)
		return ErrGetAlreadyCalled
	}

	handler(reg)

	return nil
}

// OverrideFunc ...
func (s *Locator[T]) OverrideFunc(unv *Universe, newFn func(unv *Universe) T) error {
	return s.doBeforeGet(unv, func(reg *registeredService) {
		reg.newFunc = func(unv *Universe) any {
			return newFn(unv)
		}
	})
}

func (s *Locator[T]) panicOverrideError(err error) {
	var val *T
	svcType := reflect.TypeOf(val).Elem()

	panic(fmt.Sprintf("Can NOT override service of type '%v', err: %v", svcType, err))
}

// MustOverride will fail if Override returns false
func (s *Locator[T]) MustOverride(unv *Universe, svc T) {
	err := s.Override(unv, svc)
	if err != nil {
		s.panicOverrideError(err)
	}
}

func (s *Locator[T]) MustOverrideFunc(unv *Universe, newFn func(unv *Universe) T) {
	err := s.OverrideFunc(unv, newFn)
	if err != nil {
		s.panicOverrideError(err)
	}
}

// Wrap the original implementation with the object created by wrapper
func (s *Locator[T]) Wrap(unv *Universe, wrapper func(unv *Universe, svc T) T) (err error) {
	return s.doBeforeGet(unv, func(reg *registeredService) {
		reg.wrappers = append(reg.wrappers, func(svc any) any {
			return wrapper(unv, svc.(T))
		})
	})
}

// MustWrap similar to Wrap, but it will panic if not succeeded
func (s *Locator[T]) MustWrap(unv *Universe, wrapper func(unv *Universe, svc T) T) {
	err := s.Wrap(unv, wrapper)
	if err != nil {
		var val *T

		str := fmt.Sprintf(
			"Failed to Wrap '%v', error: %v",
			reflect.TypeOf(val).Elem(),
			err,
		)
		panic(str)
	}
}

var notAllowRegistering atomic.Bool

// Register creates a new Locator allow to call Get to create a new object
func Register[T any](newFn func(unv *Universe) T) *Locator[T] {
	if notAllowRegistering.Load() {
		panic("Not allow Register being called after PreventRegistering")
	}
	key := new(T)
	return &Locator[T]{
		key:   key,
		newFn: newFn,
	}
}

// RegisterSimple creates a new Locator with very simple newFn that returns the zero value
func RegisterSimple[T any]() *Locator[T] {
	return Register[T](func(unv *Universe) T {
		var empty T
		return empty
	})
}

// RegisterEmpty does not init anything when calling Get, and must be Override
func RegisterEmpty[T any]() *Locator[T] {
	if notAllowRegistering.Load() {
		panic("Not allow Register being called after PreventRegistering")
	}

	key := new(T)
	var val *T

	return &Locator[T]{
		key: key,
		newFn: func(unv *Universe) T {
			panic(
				fmt.Sprintf(
					"Not found registered object of type '%v'",
					reflect.TypeOf(val).Elem(),
				),
			)
		},
	}
}

// PreventRegistering prevents Register* functions being called after main() is started
func PreventRegistering() {
	notAllowRegistering.Store(true)
}

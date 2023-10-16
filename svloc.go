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
	data *universeData

	reg  *registeredService
	prev *Universe // linked list of Universe
}

// universeData is the real global structure
// Universe just a local context object
type universeData struct {
	mut    sync.Mutex
	svcMap map[any]*registeredService

	getCallList     []*registeredService // list of Get calls from earliest to latest
	alreadyShutdown bool
}

// NewUniverse creates a new Universe
func NewUniverse() *Universe {
	return &Universe{
		data: &universeData{
			svcMap: map[any]*registeredService{},
		},

		reg:  nil,
		prev: nil,
	}
}

type registeredService struct {
	// ===========================================
	// constant values
	key             any
	originalNewFunc func(unv *Universe) any
	// ===========================================

	mut sync.Mutex

	onceDone atomic.Bool // similar to sync.Once, but add a mechanism for detecting deadlock

	svc             any
	getCallLocation string
	createUnv       *Universe

	newFunc  func(unv *Universe) any
	wrappers []func(unv *Universe, svc any) any

	alreadyShutdown bool
	onShutdown      func()
}

func (s *registeredService) newService(unv *Universe, callLoc string) any {
	if s.onceDone.Load() {
		return s.svc
	}

	svc := s.newServiceSlow(unv, callLoc)

	unv.data.appendGetCall(s)

	return svc
}

func (s *registeredService) callNewFuncAndWrappers(unv *Universe) {
	newFunc := s.originalNewFunc
	if s.newFunc != nil {
		newFunc = s.newFunc
	}

	newUnv := &Universe{
		data: unv.data,

		reg:  s,
		prev: unv,
	}

	s.svc = newFunc(newUnv)
	s.createUnv = newUnv

	for _, wrapper := range s.wrappers {
		s.svc = wrapper(newUnv, s.svc)
	}
}

func (s *registeredService) newServiceSlow(unv *Universe, callLoc string) any {
	ok := s.mut.TryLock()
	if !ok {
		if unv.detectedCircularDependency(s.key, callLoc) {
			panic("svloc: circular dependency detected")
		}
		s.mut.Lock()
	}
	defer s.mut.Unlock()

	// double-checked locking
	if s.onceDone.Load() {
		return s.svc
	}

	s.getCallLocation = callLoc

	s.callNewFuncAndWrappers(unv)

	s.onceDone.Store(true)

	return s.svc
}

func (s *registeredService) doShutdown() {
	s.mut.Lock()
	defer s.mut.Unlock()

	if s.alreadyShutdown {
		return
	}

	if s.onShutdown != nil {
		s.onShutdown()
	}

	s.alreadyShutdown = true
}

func (u *Universe) printGetCallTrace(problem string, callLoc string) {
	fmt.Println("==========================================================")
	fmt.Printf("%s:\n", problem)

	if callLoc != "" {
		fmt.Println("\t" + callLoc)
	}

	for current := u; current != nil; current = current.prev {
		reg := current.reg
		if reg == nil {
			break
		}
		fmt.Println("\t" + reg.getCallLocation)
	}

	fmt.Println("==========================================================")
}

func (u *Universe) detectedCircularDependency(newKey any, callLoc string) bool {
	for currentUnv := u; currentUnv != nil; currentUnv = currentUnv.prev {
		reg := currentUnv.reg
		if reg == nil {
			break
		}

		if reg.key != newKey {
			continue
		}

		u.printGetCallTrace("Get calls stacktrace that causes circular dependency", callLoc)
		return true
	}
	return false
}

func (u *universeData) getService(key any, originalNewFunc func(unv *Universe) any) *registeredService {
	u.mut.Lock()
	defer u.mut.Unlock()

	svc, existed := u.svcMap[key]
	if existed {
		return svc
	}

	svc = &registeredService{
		key:             key,
		originalNewFunc: originalNewFunc,
	}
	u.svcMap[key] = svc

	return svc
}

func (u *universeData) appendGetCall(s *registeredService) {
	u.mut.Lock()
	defer u.mut.Unlock()

	if u.alreadyShutdown {
		panic("svloc: can NOT Get after Shutdown")
	}
	u.getCallList = append(u.getCallList, s)
}

// Locator is a Thread-Safe object and can be called in multiple goroutines
type Locator[T any] struct {
	key   *T
	newFn func(unv *Universe) any
}

// Get ...
func (s *Locator[T]) Get(unv *Universe) T {
	reg := unv.data.getService(s.key, s.newFn)

	_, file, line, _ := runtime.Caller(1)
	loc := fmt.Sprintf("%s:%d", file, line)

	svc := reg.newService(unv, loc)
	result, ok := svc.(T)
	if !ok {
		var empty T
		return empty
	}
	return result
}

// Override prevents running the function inside Register
func (s *Locator[T]) Override(unv *Universe, svc T) error {
	return s.OverrideFunc(unv, func(unv *Universe) T {
		return svc
	})
}

func (s *Locator[T]) doBeforeGet(unv *Universe, handler func(reg *registeredService)) error {
	reg := unv.data.getService(s.key, s.newFn)

	reg.mut.Lock()
	defer reg.mut.Unlock()

	if reg.onceDone.Load() {
		reg.createUnv.printGetCallTrace("Get calls stacktrace", "")
		return ErrGetAlreadyCalled
	}

	handler(reg)

	return nil
}

// OverrideFunc ...
func (s *Locator[T]) OverrideFunc(unv *Universe, newFn func(unv *Universe) T) error {
	if unv.prev != nil {
		return errOverrideInsideNewFunctions
	}
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

// MustOverrideFunc similar to OverrideFunc but panics if error returned
func (s *Locator[T]) MustOverrideFunc(unv *Universe, newFn func(unv *Universe) T) {
	err := s.OverrideFunc(unv, newFn)
	if err != nil {
		s.panicOverrideError(err)
	}
}

// Wrap the original implementation with the object created by wrapper
func (s *Locator[T]) Wrap(unv *Universe, wrapper func(unv *Universe, svc T) T) (err error) {
	return s.doBeforeGet(unv, func(reg *registeredService) {
		reg.wrappers = append(reg.wrappers, func(unv *Universe, svc any) any {
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

// OnShutdown must only be called inside 'new' functions
// It will panic if called outside
func (u *Universe) OnShutdown(fn func()) {
	if u.prev == nil {
		panic("svloc: can NOT call OnShutdown outside new functions")
	}
	u.reg.onShutdown = fn
}

func (u *universeData) cloneRegList() []*registeredService {
	u.mut.Lock()
	defer u.mut.Unlock()

	regList := make([]*registeredService, len(u.getCallList))
	copy(regList, u.getCallList)

	u.alreadyShutdown = true
	return regList
}

// Shutdown call each callback that registered by  OnShutdown
// This function must only be called outside the 'new' functions
// It will panic if called inside
func (u *Universe) Shutdown() {
	regList := u.data.cloneRegList()

	for i := len(regList) - 1; i >= 0; i-- {
		reg := regList[i]
		reg.doShutdown()
	}
}

var notAllowRegistering atomic.Bool

func checkAllowRegistering() {
	if notAllowRegistering.Load() {
		panic("Not allow Register* function being called after PreventRegistering")
	}
}

// Register creates a new Locator allow to call Get to create a new object
func Register[T any](newFn func(unv *Universe) T) *Locator[T] {
	checkAllowRegistering()

	key := new(T)
	return &Locator[T]{
		key: key,
		newFn: func(unv *Universe) any {
			return newFn(unv)
		},
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
	checkAllowRegistering()

	key := new(T)
	var val *T

	return &Locator[T]{
		key: key,
		newFn: func(unv *Universe) any {
			unv.printGetCallTrace("Get call stacktrace", "")
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

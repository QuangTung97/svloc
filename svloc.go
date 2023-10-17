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

	cleared bool

	shutdownFuncs   []func() // list of shutdown funcs from earliest to latest
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

// CleanUp removes the data for wiring services
// after that, all calls will panic, excepts for Shutdown
func (u *Universe) CleanUp() {
	u.data.mut.Lock()

	u.data.svcMap = nil

	defer u.data.mut.Unlock()

	u.data.cleared = true
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

	overrideCallLocation string

	newFunc func(unv *Universe) any

	wrappers    []func(unv *Universe, svc any) any
	wrapperLocs []string

	onShutdown func()
}

func (s *registeredService) newService(unv *Universe, callLoc string) any {
	if s.onceDone.Load() {
		return s.svc
	}

	svc := s.newServiceSlow(unv, callLoc)

	unv.data.appendShutdownFunc(s.onShutdown)

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

func (u *universeData) getService(
	key any, originalNewFunc func(unv *Universe) any,
	methodName string,
) (*registeredService, error) {
	u.mut.Lock()
	defer u.mut.Unlock()

	if u.cleared {
		return nil, fmt.Errorf("svloc: can NOT call '%s' after 'CleanUp'", methodName)
	}

	svc, existed := u.svcMap[key]
	if existed {
		return svc, nil
	}

	svc = &registeredService{
		key:             key,
		originalNewFunc: originalNewFunc,
	}
	u.svcMap[key] = svc

	return svc, nil
}

func (u *universeData) appendShutdownFunc(fn func()) {
	u.mut.Lock()
	defer u.mut.Unlock()

	if u.alreadyShutdown {
		panic("svloc: can NOT call 'Get' after 'Shutdown'")
	}

	if fn == nil {
		return
	}
	u.shutdownFuncs = append(u.shutdownFuncs, fn)
}

// Locator is a Thread-Safe object and can be called in multiple goroutines
type Locator[T any] struct {
	key   *T
	newFn func(unv *Universe) any

	registerLoc string
}

// Get can panic if Shutdown already called
func (s *Locator[T]) Get(unv *Universe) T {
	reg, err := unv.data.getService(s.key, s.newFn, "Get")
	if err != nil {
		panic(err.Error())
	}

	loc := getCallerLocation()

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
	return s.overrideFuncWithLoc(unv, func(unv *Universe) T {
		return svc
	}, getCallerLocation())
}

func (s *Locator[T]) doBeforeGet(
	unv *Universe,
	methodName string,
	handler func(reg *registeredService),
) error {
	reg, err := unv.data.getService(s.key, s.newFn, methodName)
	if err != nil {
		return err
	}

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
	return s.overrideFuncWithLoc(unv, newFn, getCallerLocation())
}

func (s *Locator[T]) overrideFuncWithLoc(
	unv *Universe, newFn func(unv *Universe) T,
	callLoc string,
) error {
	if unv.prev != nil {
		return errOverrideInsideNewFunctions
	}
	return s.doBeforeGet(unv, "Override", func(reg *registeredService) {
		reg.overrideCallLocation = callLoc
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
	err := s.overrideFuncWithLoc(unv, func(unv *Universe) T {
		return svc
	}, getCallerLocation())
	if err != nil {
		s.panicOverrideError(err)
	}
}

// MustOverrideFunc similar to OverrideFunc but panics if error returned
func (s *Locator[T]) MustOverrideFunc(unv *Universe, newFn func(unv *Universe) T) {
	err := s.overrideFuncWithLoc(unv, newFn, getCallerLocation())
	if err != nil {
		s.panicOverrideError(err)
	}
}

// Wrap the original implementation with the object created by wrapper
func (s *Locator[T]) Wrap(unv *Universe, wrapper func(unv *Universe, svc T) T) (err error) {
	return s.wrapWithLoc(unv, wrapper, getCallerLocation())
}

func (s *Locator[T]) wrapWithLoc(
	unv *Universe, wrapper func(unv *Universe, svc T) T,
	callLoc string,
) (err error) {
	return s.doBeforeGet(unv, "Wrap", func(reg *registeredService) {
		reg.wrappers = append(reg.wrappers, func(unv *Universe, svc any) any {
			return wrapper(unv, svc.(T))
		})
		reg.wrapperLocs = append(reg.wrapperLocs, callLoc)
	})
}

// MustWrap similar to Wrap, but it will panic if not succeeded
func (s *Locator[T]) MustWrap(unv *Universe, wrapper func(unv *Universe, svc T) T) {
	err := s.wrapWithLoc(unv, wrapper, getCallerLocation())
	if err != nil {
		var val *T

		str := fmt.Sprintf(
			"Failed to Wrap '%v', err: %v",
			reflect.TypeOf(val).Elem(),
			err,
		)
		panic(str)
	}
}

// GetLastOverrideLocation returns the last location that Override* is called
// if no Override* is called, returns the Register location
func (s *Locator[T]) GetLastOverrideLocation(unv *Universe) (string, error) {
	reg, err := unv.data.getService(s.key, s.newFn, "GetLastOverrideLocation")
	if err != nil {
		return "", err
	}

	reg.mut.Lock()
	defer reg.mut.Unlock()

	if reg.overrideCallLocation != "" {
		return reg.overrideCallLocation, nil
	}
	return s.registerLoc, nil
}

// GetWrapLocations returns Wrap* call's locations
func (s *Locator[T]) GetWrapLocations(unv *Universe) ([]string, error) {
	reg, err := unv.data.getService(s.key, s.newFn, "GetWrapLocations")
	if err != nil {
		return nil, err
	}

	reg.mut.Lock()
	defer reg.mut.Unlock()

	locs := make([]string, len(reg.wrapperLocs))
	copy(locs, reg.wrapperLocs)

	return locs, nil
}

// OnShutdown must only be called inside 'new' functions
// It will panic if called outside
func (u *Universe) OnShutdown(fn func()) {
	if u.prev == nil {
		panic("svloc: can NOT call OnShutdown outside new functions")
	}
	u.reg.onShutdown = fn
}

func (u *universeData) cloneShutdownFuncList() []func() {
	u.mut.Lock()
	defer u.mut.Unlock()

	if u.alreadyShutdown {
		return nil
	}

	funcList := make([]func(), len(u.shutdownFuncs))
	copy(funcList, u.shutdownFuncs)

	u.shutdownFuncs = nil
	u.alreadyShutdown = true
	return funcList
}

// Shutdown call each callback that registered by OnShutdown
// This function must only be called outside the 'new' functions
// It will panic if called inside
func (u *Universe) Shutdown() {
	funcList := u.data.cloneShutdownFuncList()

	for i := len(funcList) - 1; i >= 0; i-- {
		fn := funcList[i]
		fn()
	}
}

var notAllowRegistering atomic.Bool

func checkAllowRegistering() {
	if notAllowRegistering.Load() {
		panic("Not allow Register* function being called after PreventRegistering")
	}
}

func getCallerLocation() string {
	_, file, line, _ := runtime.Caller(2)
	return fmt.Sprintf("%s:%d", file, line)
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
		registerLoc: getCallerLocation(),
	}
}

// RegisterSimple creates a new Locator with very simple newFn that returns the zero value
func RegisterSimple[T any]() *Locator[T] {
	s := Register[T](func(unv *Universe) T {
		var empty T
		return empty
	})
	s.registerLoc = getCallerLocation()
	return s
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
		registerLoc: getCallerLocation(),
	}
}

// PreventRegistering prevents Register* functions being called after main() is started
func PreventRegistering() {
	notAllowRegistering.Store(true)
}

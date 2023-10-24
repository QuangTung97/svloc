package svloc

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
)

// Universe represents a global service registry.
//
// Each newFn in a Locator[T] will be called once for each Universe.
// But different Universes can have different values of object created by *newFn* functions.
//
// The order of calls MUST be:
//
//	Register* in global => PreventRegistering() => Wrap / Override / MustOverride => Get
//
// It will return errors or panic (with Must* functions) if Locator.Get happens before any of those functions
type Universe struct {
	data *universeData

	reg  *registeredService
	prev *Universe // linked list of Universe
}

// universeData is the real global structure
//
// Universe just a local context object
type universeData struct {
	stackPrinted atomic.Bool

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

// CleanUp removes the data for wiring services. Help reduce GC overhead.
//
// After called, all other calls will panic, excepts for Shutdown
func (u *Universe) CleanUp() {
	u.data.mut.Lock()

	u.data.svcMap = nil

	defer u.data.mut.Unlock()

	u.data.cleared = true
}

type registeredService struct {
	// constant values
	loc *locatorData

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

func (s *registeredService) newService(unv *Universe) any {
	if s.onceDone.Load() {
		return s.svc
	}

	callLoc := getCallerLocationWithSkip(2)

	svc := s.newServiceSlow(unv, callLoc)

	unv.data.appendShutdownFunc(s.onShutdown)

	return svc
}

func (s *registeredService) callNewFuncAndWrappers(unv *Universe) {
	newFunc := s.loc.newFn
	if s.newFunc != nil {
		newFunc = s.newFunc
	}

	newUnv := &Universe{
		data: unv.data,

		reg:  s,
		prev: unv,
	}

	defer func() {
		if s.createUnv != nil {
			return
		}
		if !newUnv.data.stackPrinted.Swap(true) {
			newUnv.printGetCallTrace("Get calls stacktrace", "")
		}
	}()

	newSvc := newFunc(newUnv)

	for _, wrapper := range s.wrappers {
		newSvc = wrapper(newUnv, newSvc)
	}

	s.svc = newSvc
	s.createUnv = newUnv
}

func (s *registeredService) newServiceSlow(unv *Universe, callLoc string) any {
	ok := s.mut.TryLock()
	if !ok {
		if unv.detectedCircularDependency(s.loc.key, callLoc) {
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

func printSeparateLine() {
	_, _ = fmt.Fprintln(os.Stderr, "==========================================================")
}

func (u *Universe) printGetCallTrace(problem string, callLoc string) {
	printSeparateLine()
	_, _ = fmt.Fprintf(os.Stderr, "%s:\n", problem)

	if callLoc != "" {
		_, _ = fmt.Fprintln(os.Stderr, "\t"+callLoc)
	}

	for current := u; current != nil; current = current.prev {
		reg := current.reg
		if reg == nil {
			break
		}
		_, _ = fmt.Fprintln(os.Stderr, "\t"+reg.getCallLocation)
	}

	printSeparateLine()
}

func (u *Universe) detectedCircularDependency(newKey any, callLoc string) bool {
	for currentUnv := u; currentUnv != nil; currentUnv = currentUnv.prev {
		reg := currentUnv.reg
		if reg == nil {
			break
		}

		if reg.loc.key != newKey {
			continue
		}

		u.data.stackPrinted.Store(true)
		u.printGetCallTrace("Get calls stacktrace that causes circular dependency", callLoc)
		return true
	}
	return false
}

func (u *universeData) getService(
	locData *locatorData,
	methodName string,
) (*registeredService, error) {
	u.mut.Lock()
	defer u.mut.Unlock()

	if u.cleared {
		return nil, fmt.Errorf("svloc: can NOT call '%s' after 'CleanUp'", methodName)
	}

	key := locData.key

	svc, existed := u.svcMap[key]
	if existed {
		return svc, nil
	}

	svc = &registeredService{
		loc: locData,
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

// Locator is an immutable object and can be called in multiple goroutines
type Locator[T any] struct {
	data locatorData
}

type locatorData struct {
	key         *int
	newFn       func(unv *Universe) any
	registerLoc string
}

// Get can be called multiple times but the newFn inside Register* will be called ONCE.
// It can panic if Universe.Shutdown already called
func (s *Locator[T]) Get(unv *Universe) T {
	reg, err := unv.data.getService(&s.data, "Get")
	if err != nil {
		panic(err.Error())
	}

	svc := reg.newService(unv)
	result, ok := svc.(T)
	if !ok {
		var empty T
		return empty
	}
	return result
}

// Override the value returned by Get, it also prevents running of the function inside Register
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
	reg, err := unv.data.getService(&s.data, methodName)
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

// MustOverride will panic if Override returns error
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

// GetLastOverrideLocation returns the last location that Override* is called.
// If no Override* functions is called, returns the Register location
func (s *Locator[T]) GetLastOverrideLocation(unv *Universe) (string, error) {
	reg, err := unv.data.getService(&s.data, "GetLastOverrideLocation")
	if err != nil {
		return "", err
	}

	reg.mut.Lock()
	defer reg.mut.Unlock()

	if reg.overrideCallLocation != "" {
		return reg.overrideCallLocation, nil
	}
	return s.data.registerLoc, nil
}

// GetWrapLocations returns Wrap* call's locations
func (s *Locator[T]) GetWrapLocations(unv *Universe) ([]string, error) {
	reg, err := unv.data.getService(&s.data, "GetWrapLocations")
	if err != nil {
		return nil, err
	}

	reg.mut.Lock()
	defer reg.mut.Unlock()

	locs := make([]string, len(reg.wrapperLocs))
	copy(locs, reg.wrapperLocs)

	return locs, nil
}

// OnShutdown must only be called inside 'new' functions.
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

// Shutdown call each callback that registered by OnShutdown.
// This function must only be called outside the 'new' functions.
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

func getCallerLocationWithSkip(skip int) string {
	_, file, line, _ := runtime.Caller(skip + 1)
	return fmt.Sprintf("%s:%d", file, line)
}

func getCallerLocation() string {
	_, file, line, _ := runtime.Caller(2)
	return fmt.Sprintf("%s:%d", file, line)
}

// Register creates a new Locator allow to call Locator.Get to create a new object
func Register[T any](newFn func(unv *Universe) T) *Locator[T] {
	checkAllowRegistering()

	key := new(int)
	return &Locator[T]{
		data: locatorData{
			key: key,
			newFn: func(unv *Universe) any {
				return newFn(unv)
			},
			registerLoc: getCallerLocation(),
		},
	}
}

// RegisterSimple creates a new Locator with very simple newFn that returns the zero value
func RegisterSimple[T any]() *Locator[T] {
	s := Register[T](func(unv *Universe) T {
		var empty T
		return empty
	})
	s.data.registerLoc = getCallerLocation()
	return s
}

// RegisterEmpty does not init anything when calling Get, and must be Override
func RegisterEmpty[T any]() *Locator[T] {
	checkAllowRegistering()

	callLoc := getCallerLocation()

	key := new(int)
	var val *T

	return &Locator[T]{
		data: locatorData{
			key: key,
			newFn: func(unv *Universe) any {
				printSeparateLine()
				_, _ = fmt.Fprintln(os.Stderr, "'RegisterEmpty' location:", callLoc)
				panic(
					fmt.Sprintf(
						"Not found registered object of type '%v'",
						reflect.TypeOf(val).Elem(),
					),
				)
			},
			registerLoc: callLoc,
		},
	}
}

// PreventRegistering prevents Register* functions being called after main() is started
func PreventRegistering() {
	notAllowRegistering.Store(true)
}

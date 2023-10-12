package svloc

import (
	"fmt"
	"reflect"
	"sync"
)

// Universe every universe is different
// each newFn in a Locator[T] will be called once for each Universe
// but different Universes can have different values of object created by values
type Universe struct {
	mut    sync.Mutex
	svcMap map[any]*registeredService
}

// NewUniverse creates a new Universe
func NewUniverse() *Universe {
	return &Universe{
		svcMap: map[any]*registeredService{},
	}
}

type registeredService struct {
	once sync.Once
	svc  any
}

func (u *Universe) getService(key any) *registeredService {
	u.mut.Lock()
	defer u.mut.Unlock()

	svc, existed := u.svcMap[key]
	if existed {
		return svc
	}

	svc = &registeredService{}
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
	registered := unv.getService(s.key)

	registered.once.Do(func() {
		registered.svc = s.newFn(unv)
	})

	return registered.svc.(T)
}

// Override prevent running the function inside Register
func (s *Locator[T]) Override(unv *Universe, svc T) (succeeded bool) {
	registered := unv.getService(s.key)

	registered.once.Do(func() {
		succeeded = true
		registered.svc = svc
	})

	return succeeded
}

// MustOverride will fail if Override returns false
func (s *Locator[T]) MustOverride(unv *Universe, svc T) {
	var val *T
	svcType := reflect.TypeOf(val).Elem()

	ok := s.Override(unv, svc)
	if !ok {
		panic(fmt.Sprintf("Can NOT override service of type: '%v'", svcType))
	}
}

// Register creates a new Locator allow to call Get to create a new object
func Register[T any](newFn func(unv *Universe) T) *Locator[T] {
	key := new(T)
	return &Locator[T]{
		key:   key,
		newFn: newFn,
	}
}

// RegisterEmpty does not init anything when calling Get, and must be Override
func RegisterEmpty[T any]() *Locator[T] {
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

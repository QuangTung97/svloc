package svloc

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

type Repo interface {
	GetUser() string
}

type UserRepo struct {
}

func (r *UserRepo) GetUser() string {
	return "user_repo"
}

var newUserRepoCalls int

var userRepoLoc = Register[Repo](func(unv *Universe) Repo {
	newUserRepoCalls++
	return &UserRepo{}
})

type UserService struct {
	repo Repo
}

func NewService(repo Repo) *UserService {
	return &UserService{
		repo: repo,
	}
}

func (s *UserService) Hello() string {
	return "hello: " + s.repo.GetUser()
}

var userServiceLoc = Register[*UserService](func(unv *Universe) *UserService {
	repo := userRepoLoc.Get(unv)
	return NewService(repo)
})

func resetGlobals() {
	newUserRepoCalls = 0
	saveCalls = 0
}

func doGetInParallel(t *testing.T) {
	resetGlobals()

	unv := NewUniverse()

	const numThread = 3

	var wg sync.WaitGroup
	wg.Add(numThread)

	for i := 0; i < numThread; i++ {
		go func() {
			defer wg.Done()
			userRepoLoc.Get(unv)
		}()
	}

	wg.Wait()

	assert.Equal(t, 1, newUserRepoCalls)
}

func TestSimpleServiceLocator(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		resetGlobals()

		unv := NewUniverse()

		svc := userServiceLoc.Get(unv)
		assert.Equal(t, "hello: user_repo", svc.Hello())

		assert.Equal(t, 1, newUserRepoCalls)
	})

	t.Run("call multiple times", func(t *testing.T) {
		resetGlobals()

		unv := NewUniverse()

		svc := userServiceLoc.Get(unv)
		assert.Equal(t, "hello: user_repo", svc.Hello())

		newSvc := userServiceLoc.Get(unv)
		assert.Same(t, svc, newSvc)

		userRepoLoc.Get(unv)

		assert.Equal(t, 1, newUserRepoCalls)
	})

	t.Run("call multiple times in parallel", func(t *testing.T) {
		for i := 0; i < 10000; i++ {
			doGetInParallel(t)
		}
	})

	t.Run("with different universes", func(t *testing.T) {
		resetGlobals()

		unv1 := NewUniverse()
		unv2 := NewUniverse()

		svc := userServiceLoc.Get(unv1)
		assert.Equal(t, "hello: user_repo", svc.Hello())

		newSvc := userServiceLoc.Get(unv2)
		assert.NotSame(t, svc, newSvc)

		assert.Equal(t, 2, newUserRepoCalls)
	})
}

type RepoMock struct {
}

func (r *RepoMock) GetUser() string {
	return "mock"
}

func TestSimpleServiceLocator_With_Override(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		resetGlobals()

		unv := NewUniverse()
		err := userRepoLoc.Override(unv, &RepoMock{})
		assert.Equal(t, nil, err)

		svc := userServiceLoc.Get(unv)
		assert.Equal(t, "hello: mock", svc.Hello())

		assert.Equal(t, 0, newUserRepoCalls)
	})

	t.Run("failed", func(t *testing.T) {
		resetGlobals()

		unv := NewUniverse()
		userServiceLoc.Get(unv)

		err := userRepoLoc.Override(unv, &RepoMock{})
		assert.Equal(t, ErrGetAlreadyCalled, err)
	})
}

func TestSimpleServiceLocator_With_Must_Override(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		resetGlobals()

		unv := NewUniverse()

		userRepoLoc.MustOverride(unv, &RepoMock{})

		svc := userServiceLoc.Get(unv)
		assert.Equal(t, "hello: mock", svc.Hello())

		assert.Equal(t, 0, newUserRepoCalls)
	})

	t.Run("not success", func(t *testing.T) {
		resetGlobals()

		unv := NewUniverse()
		userServiceLoc.Get(unv)

		assert.PanicsWithValue(t,
			"Can NOT override service of type 'svloc.Repo', err: svloc: method Get already called",
			func() {
				userRepoLoc.MustOverride(unv, &RepoMock{})
			},
		)
	})

	t.Run("func success", func(t *testing.T) {
		resetGlobals()

		unv := NewUniverse()

		funcCalls := 0

		userRepoLoc.MustOverrideFunc(unv, func(unv *Universe) Repo {
			funcCalls++
			return &RepoMock{}
		})

		assert.Equal(t, 0, funcCalls)

		svc := userServiceLoc.Get(unv)
		assert.Equal(t, "hello: mock", svc.Hello())

		assert.Equal(t, 1, funcCalls)
	})

	t.Run("func failed", func(t *testing.T) {
		resetGlobals()

		unv := NewUniverse()

		userServiceLoc.Get(unv)

		assert.PanicsWithValue(
			t,
			"Can NOT override service of type 'svloc.Repo', err: svloc: method Get already called",
			func() {
				userRepoLoc.MustOverrideFunc(unv, func(unv *Universe) Repo {
					return &RepoMock{}
				})
			},
		)
	})
}

type Database struct {
}

var saveCalls = 0

func (db *Database) Save() {
	saveCalls++
}

var databaseLoc = RegisterEmpty[*Database]()

type ProductRepo struct {
	db *Database
}

func NewProductRepo(db *Database) *ProductRepo {
	return &ProductRepo{
		db: db,
	}
}

func (r *ProductRepo) Insert() {
	r.db.Save()
}

var productRepoLoc = Register[*ProductRepo](func(unv *Universe) *ProductRepo {
	db := databaseLoc.Get(unv)
	return NewProductRepo(db)
})

func TestRegisterEmpty(t *testing.T) {
	t.Run("db is empty", func(t *testing.T) {
		resetGlobals()

		unv := NewUniverse()

		assert.PanicsWithValue(t,
			"Not found registered object of type '*svloc.Database'",
			func() {
				productRepoLoc.Get(unv)
			},
		)
	})

	t.Run("db is non empty", func(t *testing.T) {
		resetGlobals()

		unv := NewUniverse()
		err := databaseLoc.Override(unv, &Database{})
		assert.Equal(t, nil, err)

		r := productRepoLoc.Get(unv)
		r.Insert()

		assert.Equal(t, 1, saveCalls)
	})
}

type WrapperRepo struct {
	repo   Repo
	prefix string
}

func (r *WrapperRepo) GetUser() string {
	return r.prefix + ": " + r.repo.GetUser()
}

func TestLocator_Wrap(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		unv := NewUniverse()

		err := userRepoLoc.Wrap(unv, func(unv *Universe, repo Repo) Repo {
			return &WrapperRepo{repo: repo, prefix: "wrapped"}
		})
		assert.Equal(t, nil, err)

		svc := userServiceLoc.Get(unv)
		assert.Equal(t, "hello: wrapped: user_repo", svc.Hello())
	})

	t.Run("multi wrappers", func(t *testing.T) {
		unv := NewUniverse()

		userRepoLoc.MustWrap(unv, func(unv *Universe, repo Repo) Repo {
			return &WrapperRepo{repo: repo, prefix: "wrapper01"}
		})
		userRepoLoc.MustWrap(unv, func(unv *Universe, repo Repo) Repo {
			return &WrapperRepo{repo: repo, prefix: "wrapper02"}
		})

		svc := userServiceLoc.Get(unv)
		assert.Equal(t, "hello: wrapper02: wrapper01: user_repo", svc.Hello())
	})

	t.Run("failed wrap after get", func(t *testing.T) {
		unv := NewUniverse()

		userServiceLoc.Get(unv)

		assert.PanicsWithValue(
			t, "Failed to Wrap 'svloc.Repo', error: svloc: method Get already called",
			func() {
				userRepoLoc.MustWrap(unv, func(unv *Universe, repo Repo) Repo {
					return &WrapperRepo{repo: repo, prefix: "wrapper"}
				})
			},
		)
	})
}

type serviceA struct {
	ref *serviceB
}

type serviceB struct {
	ref *serviceA
}

type complexServiceA struct {
	ref *complexServiceB
}

type complexServiceB struct {
	ref *complexServiceC
}

type complexServiceC struct {
	ref *complexServiceA
}

var serviceALoc = RegisterEmpty[*serviceA]()

var serviceBLoc = RegisterEmpty[*serviceB]()

var complexServiceALoc = RegisterEmpty[*complexServiceA]()

var complexServiceBLoc = RegisterEmpty[*complexServiceB]()

var complexServiceCLoc = RegisterEmpty[*complexServiceC]()

func TestLocator_Detect_Circular_Dependency(t *testing.T) {
	t.Run("two services", func(t *testing.T) {
		unv := NewUniverse()

		serviceALoc.MustOverrideFunc(unv, func(unv *Universe) *serviceA {
			return &serviceA{
				ref: serviceBLoc.Get(unv),
			}
		})

		serviceBLoc.MustOverrideFunc(unv, func(unv *Universe) *serviceB {
			return &serviceB{
				ref: serviceALoc.Get(unv),
			}
		})

		assert.PanicsWithValue(t, "svloc: circular dependency detected", func() {
			serviceALoc.Get(unv)
		})
	})

	t.Run("complex", func(t *testing.T) {
		unv := NewUniverse()

		complexServiceALoc.MustOverrideFunc(unv, func(unv *Universe) *complexServiceA {
			return &complexServiceA{
				ref: complexServiceBLoc.Get(unv),
			}
		})

		complexServiceBLoc.MustOverrideFunc(unv, func(unv *Universe) *complexServiceB {
			return &complexServiceB{
				ref: complexServiceCLoc.Get(unv),
			}
		})

		complexServiceCLoc.MustOverrideFunc(unv, func(unv *Universe) *complexServiceC {
			return &complexServiceC{
				ref: complexServiceALoc.Get(unv),
			}
		})

		assert.PanicsWithValue(t, "svloc: circular dependency detected", func() {
			complexServiceCLoc.Get(unv)
		})
	})
}

var userRepoSimpleLoc = RegisterSimple[Repo]()

var userRepoStructLoc = RegisterSimple[UserRepo]()

func TestLocator_Register_Simple(t *testing.T) {
	t.Run("interface", func(t *testing.T) {
		unv := NewUniverse()
		val := userRepoSimpleLoc.Get(unv)
		assert.Equal(t, nil, val)
	})

	t.Run("struct", func(t *testing.T) {
		unv := NewUniverse()
		val := userRepoStructLoc.Get(unv)
		assert.Equal(t, UserRepo{}, val)
	})
}

func TestPreventRegistering(t *testing.T) {
	t.Run("register", func(t *testing.T) {
		defer func() {
			notAllowRegistering.Store(false)
		}()

		PreventRegistering()

		assert.PanicsWithValue(t,
			"Not allow Register* function being called after PreventRegistering",
			func() {
				Register[*UserService](func(unv *Universe) *UserService {
					return &UserService{}
				})
			},
		)
	})

	t.Run("register empty", func(t *testing.T) {
		defer func() {
			notAllowRegistering.Store(false)
		}()

		PreventRegistering()

		assert.PanicsWithValue(t,
			"Not allow Register* function being called after PreventRegistering",
			func() {
				RegisterEmpty[*UserService]()
			},
		)
	})
}

type productRepo struct {
}

type terminalRepo struct {
}

type productService struct {
	repo     *productRepo
	terminal *terminalRepo
}

type productHandler struct {
	svc *productService
}

func TestLocator_Register_Complex__Success(t *testing.T) {
	initRepoCalls := 0
	var initRepoUnv *Universe
	repoLoc := Register[*productRepo](func(unv *Universe) *productRepo {
		initRepoCalls++
		initRepoUnv = unv
		return &productRepo{}
	})

	initTerminalCalls := 0
	terminalRepoLoc := Register[*terminalRepo](func(unv *Universe) *terminalRepo {
		initTerminalCalls++
		return &terminalRepo{}
	})

	initSvcCalls := 0
	var initSvcUnv *Universe
	svcLoc := Register[*productService](func(unv *Universe) *productService {
		initSvcCalls++
		initSvcUnv = unv
		return &productService{
			repo:     repoLoc.Get(unv),
			terminal: terminalRepoLoc.Get(unv),
		}
	})

	initHandlerCalls := 0
	handlerLoc := Register[*productHandler](func(unv *Universe) *productHandler {
		initHandlerCalls++
		return &productHandler{
			svc: svcLoc.Get(unv),
		}
	})

	unv := NewUniverse()

	assert.Same(t, svcLoc.Get(unv), svcLoc.Get(unv))
	assert.Same(t, handlerLoc.Get(unv), handlerLoc.Get(unv))

	h := handlerLoc.Get(unv)
	assert.Same(t, h.svc.repo, repoLoc.Get(unv))

	assert.Equal(t, 1, initHandlerCalls)
	assert.Equal(t, 1, initSvcCalls)
	assert.Equal(t, 1, initTerminalCalls)
	assert.Equal(t, 1, initRepoCalls)

	assert.NotSame(t, initRepoUnv, initSvcUnv)
}

func doGetAndOverrideInParallel(_ *testing.T) {
	resetGlobals()

	unv := NewUniverse()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		userRepoLoc.Get(unv)
	}()

	go func() {
		defer wg.Done()
		_ = userRepoLoc.Override(unv, &RepoMock{})
	}()

	wg.Wait()
}

func TestLocator_Get_And_Override_In_Parallel(t *testing.T) {
	for i := 0; i < 10_000; i++ {
		doGetAndOverrideInParallel(t)
	}
}

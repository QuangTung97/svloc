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
		ok := userRepoLoc.Override(unv, &RepoMock{})
		assert.Equal(t, true, ok)

		svc := userServiceLoc.Get(unv)
		assert.Equal(t, "hello: mock", svc.Hello())

		assert.Equal(t, 0, newUserRepoCalls)
	})

	t.Run("failed", func(t *testing.T) {
		resetGlobals()

		unv := NewUniverse()
		userServiceLoc.Get(unv)

		ok := userRepoLoc.Override(unv, &RepoMock{})
		assert.Equal(t, false, ok)
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

		assert.PanicsWithValue(t, "Can NOT override service of type: 'svloc.Repo'", func() {
			userRepoLoc.MustOverride(unv, &RepoMock{})
		})
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
		databaseLoc.Override(unv, &Database{})

		r := productRepoLoc.Get(unv)
		r.Insert()

		assert.Equal(t, 1, saveCalls)
	})
}

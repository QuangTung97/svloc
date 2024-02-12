# A Library for Dependency Injection in Go

[![svloc](https://github.com/QuangTung97/svloc/actions/workflows/go.yml/badge.svg)](https://github.com/QuangTung97/svloc/actions/workflows/go.yml)
[![Coverage Status](https://coveralls.io/repos/github/QuangTung97/svloc/badge.svg?branch=master)](https://coveralls.io/github/QuangTung97/svloc?branch=master)

## Why this library?

* It is simpler than [uber/fx](https://github.com/uber-go/fx), yet still powerful
* Static typing & without using reflection
* Easy to use and easy to replace objects for unit & integration testing (by using **Override**)
* Easy to read & navigate with IDEs
* Safe to use in multiple goroutines
* Very easy-to-read stacktraces when something wrong happens
* Provide default wiring through the **Register** method, or no default wiring through **RegisterEmpty**

#### This library does NOT actually use the Service Locator pattern

### Limitations

* Runtime wiring of objects
* Deep stack calls
* ``Override`` functions only work outside of the ``anonymous functions`` in the ``Register`` calls
## Installtion

```bash
go get github.com/QuangTung97/svloc@v0.5.0
```

## Examples

Assume ``RepoImpl`` implements interface ``Repo``:

```go
package main

type Repo interface {
	GetUser() string
}

type RepoImpl struct {
	name string
}

func NewRepo(name string) *RepoImpl {
	return &RepoImpl{
		name: name,
	}
}

func (r *RepoImpl) GetUser() string {
	return r.name
}
```

Assume ``Service`` with method ``Hello``:

```go
package main

import (
	"fmt"
)

type Service struct {
	rp Repo
}

func NewService(repo Repo) *Service {
	return &Service{
		rp: repo,
	}
}

func (s *Service) Hello() {
	fmt.Println("Hello User:", s.rp.GetUser())
}
```

We can create ``Locator[T]`` objects:

```go
package main

import (
	"github.com/QuangTung97/svloc"
)

var usernameLoc = svloc.RegisterEmpty[string]()

var repoLoc = svloc.Register[Repo](func(unv *svloc.Universe) Repo {
	return NewRepo(
		usernameLoc.Get(unv),
	)
})

var serviceLoc = svloc.Register[*Service](func(unv *svloc.Universe) *Service {
	return NewService(repoLoc.Get(unv))
})
```

The 3 newly created objects: ``usernameLoc``, ``repoLoc``, ``serviceLoc``
are all immutable objects and safe to use concurrently.

The ``svloc.PreventRegistering`` will not allow ``Register`` functions to be called after that point.
Usually at the start of the ``main()`` function.

To use in ``main()``, first creates a new ``Universe``.
Then call ``MustOverride()`` on ``usernameLoc`` to provide the username string.
And then call the ``serviceLoc.Get()`` with that ``Universe``,
All of the wiring will happen automatically:

```go
package main

func main() {
	svloc.PreventRegistering()

	unv := svloc.NewUniverse()
	usernameLoc.MustOverride(unv, "user01")

	svc := serviceLoc.Get(unv)
	svc.Hello()
}
```

Full example:

```go
package main

import (
	"fmt"
	"github.com/QuangTung97/svloc"
)

type Repo interface {
	GetUser() string
}

type RepoImpl struct {
	name string
}

func NewRepo(name string) *RepoImpl {
	return &RepoImpl{
		name: name,
	}
}

func (r *RepoImpl) GetUser() string {
	return r.name
}

type Service struct {
	rp Repo
}

func NewService(repo Repo) *Service {
	return &Service{
		rp: repo,
	}
}

func (s *Service) Hello() {
	fmt.Println("Hello User:", s.rp.GetUser())
}

var usernameLoc = svloc.RegisterEmpty[string]()

var repoLoc = svloc.Register[Repo](func(unv *svloc.Universe) Repo {
	return NewRepo(
		usernameLoc.Get(unv),
	)
})

var serviceLoc = svloc.Register[*Service](func(unv *svloc.Universe) *Service {
	return NewService(repoLoc.Get(unv))
})

func main() {
	svloc.PreventRegistering()

	unv := svloc.NewUniverse()
	usernameLoc.MustOverride(unv, "user01")

	svc := serviceLoc.Get(unv)
	svc.Hello()
}
```

Using ``OnShutdown`` and ``Shutdown``:

```go
package main

var repoLoc = svloc.Register[Repo](func(unv *svloc.Universe) Repo {
	unv.OnShutdown(func() {
		fmt.Println("Shutdown Repo")
	})

	return NewRepo(
		usernameLoc.Get(unv),
	)
})

var serviceLoc = svloc.Register[*Service](func(unv *svloc.Universe) *Service {
	unv.OnShutdown(func() {
		fmt.Println("Shutdown Service")
	})
	return NewService(repoLoc.Get(unv))
})

func main() {
	svloc.PreventRegistering()

	unv := svloc.NewUniverse()
	defer unv.Shutdown()

	usernameLoc.MustOverride(unv, "user01")

	svc := serviceLoc.Get(unv)
	svc.Hello()
}
```

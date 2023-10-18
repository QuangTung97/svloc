# Service Locator Library for Dependency Injection in Go

#### (Not actually the Service Locator pattern)

[![svloc](https://github.com/QuangTung97/svloc/actions/workflows/go.yml/badge.svg)](https://github.com/QuangTung97/svloc/actions/workflows/go.yml)
[![Coverage Status](https://coveralls.io/repos/github/QuangTung97/svloc/badge.svg?branch=master)](https://coveralls.io/github/QuangTung97/svloc?branch=master)

## Why this library?

* It is simpler than [uber/fx](https://github.com/uber-go/fx), yet still powerful
* Static typing
* Easy to use and easy to replace objects for unit testing / integration testing

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

Add use in ``main()``, by first creates a new ``Universe``. 
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
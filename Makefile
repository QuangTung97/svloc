.PHONY: test test-race

test:
	go test ./...

test-race:
	go test -count=1 -race ./...
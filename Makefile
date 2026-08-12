# agentmon — always run fresh so you never watch a stale build.
.PHONY: run build test vet

run:            ## build+run from source (use this, not a stale ./agentmon)
	go run .

build:          ## produce ./agentmon (gitignored)
	go build -o agentmon .

test:
	go test ./...

vet:
	go vet ./...

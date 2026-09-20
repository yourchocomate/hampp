# Common development tasks. See CONTRIBUTING.md.
IMAGE      ?= hampp-dev
CROSS      ?= hampp-cross
DOCKER_RUN  = docker run --rm

.PHONY: test lint snapshots docker-dev shell e2e cross-image release-snapshot clean

test: ## unit tests with the race detector
	go test -race ./...

lint: ## formatting and vet
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	go vet ./...

snapshots: ## refresh TUI screen snapshots after an intentional UI change
	go test ./internal/tui -update

docker-dev: ## build the Termux dev image
	docker build --target dev -t $(IMAGE) .

shell: docker-dev ## interactive Termux shell with hampp; source mounted at /src
	docker run -it --rm -p 8080:8080 -p 8443:8443 -v "$(CURDIR):/src" $(IMAGE)

e2e: docker-dev ## full-stack smoke test in real Termux packages
	$(DOCKER_RUN) $(IMAGE) bash test/e2e/smoke.sh

cross-image: ## Linux + Android NDK r29 + goreleaser (linux/amd64)
	docker build --target cross -t $(CROSS) .

release-snapshot: cross-image ## build release binaries and .deb files into dist/
	$(DOCKER_RUN) -v "$(CURDIR):/src" $(CROSS) \
		bash -c 'git config --global --add safe.directory /src && goreleaser release --snapshot --clean'

clean:
	rm -rf dist

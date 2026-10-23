# Everything a developer runs goes through here. CI runs `make check` and the
# image builds; nothing else is special.
CMDS := api fixtures
GO   := cd backend &&

.PHONY: run build test check fmt tidy tidy-check lint ui ui-check e2e env keys up down logs migrate brand-check games fixtures

run:            ## go run ./cmd/$(C) (default api)
	$(GO) go run ./cmd/$(or $(C),api)

build:          ## static binaries into backend/bin/
	@for c in $(CMDS); do echo "building $$c"; (cd backend && CGO_ENABLED=0 go build -trimpath -o bin/$$c ./cmd/$$c) || exit 1; done

test:           ## the Go tests, race detector on
	$(GO) go test -race -count=1 ./...

check: fmt tidy-check brand-check ui-check  ## what CI runs
	$(GO) go vet ./...
	$(GO) go test -race -count=1 ./...

fmt:            ## fail on unformatted Go
	@out="$$(gofmt -l backend)"; if [ -n "$$out" ]; then echo "gofmt:"; echo "$$out"; exit 1; fi

tidy:
	$(GO) go mod tidy

tidy-check:     ## go.mod and go.sum are tidy
	@before="$$(shasum backend/go.mod backend/go.sum)"; (cd backend && go mod tidy); after="$$(shasum backend/go.mod backend/go.sum)"; \
	if [ "$$before" != "$$after" ]; then echo "go mod tidy changed go.mod/go.sum — commit them"; exit 1; fi

lint:           ## golangci-lint (CI runs it; install locally with go install)
	$(GO) golangci-lint run ./...

ui:             ## build the console
	cd web/console && npm ci && npm run build

ui-check:       ## the console and the docs build
	@for d in web/console web/docs; do \
	  if [ -f $$d/package.json ]; then \
	    (cd $$d && { [ -d node_modules ] || npm ci --silent; } && npm run build --silent >/dev/null) || exit 1; \
	  fi; done

e2e:            ## the sweep against a running stack (make up first)
	$(GO) go test -tags e2e -count=1 -v ./cmd/e2e/...

env:            ## backend/.env from the example, with a fresh signing key
	@if [ -f backend/.env ]; then echo "backend/.env exists; delete it first"; exit 1; fi
	@cp backend/.env.example backend/.env && $(MAKE) -s keys >> backend/.env && echo "wrote backend/.env"

keys:           ## a new P-256 signing key as one line
	@printf 'JWT_PRIVATE_KEY=%s\n' "$$(openssl ecparam -name prime256v1 -genkey -noout | openssl pkcs8 -topk8 -nocrypt -outform DER | base64 | tr -d '\n')"

up:             ## the laptop stack
	docker compose -f deploy/compose.yml up -d --build

down:
	docker compose -f deploy/compose.yml down

logs:           ## follow one service's log (S=api)
	docker compose -f deploy/compose.yml logs -f $(or $(S),api)

migrate:        ## apply the migrations by hand (the api does it on start)
	cd backend && go run github.com/pressly/goose/v3/cmd/goose@v3.29.0 -dir internal/data/migrations postgres "$(DATABASE_URL)" up

brand-check:    ## no borrowed brand identifier anywhere in this repo
	@scripts/brand-check.sh

# The fixtures # The games live outside this repo, one folder each in the workspace's
# gathering_games/: xo and tap are served as they are, the arcade's seven are
# projects of their own built into their dist/. The fixtures serve that folder.
GAMES_DIR := ../gathering_games
ARCADE := outsider mafia charades most-likely reflex spot-it giant-uno

games:          ## build the arcade's seven games in ../gathering_games, each into its dist/
	@for g in $(ARCADE); do \
	  (cd $(GAMES_DIR)/$$g && bun install --frozen-lockfile && bun run build) || exit 1; \
	  echo "games: $$g → $(GAMES_DIR)/$$g/dist"; \
	done
	@scripts/brand-check.sh $(GAMES_DIR)

fixtures:       ## the fixtures on :8091 over ../gathering_games (PUBLIC_URL, API_URL, PARTNER_CLIENT_SECRET from the shell)
	$(GO) FIXTURES_DIR=$(abspath $(GAMES_DIR)) go run ./cmd/fixtures

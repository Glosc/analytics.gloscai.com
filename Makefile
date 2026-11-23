.PHONY: dev vite-dev go-dev

dev:
	$(MAKE) -j2 vite-dev go-dev

vite-dev:
	cd src && npm run dev

go-dev:
	cd backend && ConnectionStrings__mailcatcher="$${ConnectionStrings__mailcatcher:-smtp://localhost:1025}" go run . -dev

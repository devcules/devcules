module github.com/devcules/devcules/web

go 1.27

// A nested module so the main module's ./... patterns never descend
// into web/node_modules (npm trees can contain Go code, e.g.
// flatted's golang port, which would trip the coverage gate).
// generate.go carries //go:build ignore: run it with
// go run web/icons/generate.go from the repo root.

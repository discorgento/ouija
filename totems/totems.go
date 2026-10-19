// Package totems imports every registered totem so its init() runs.
// When you add a new totem directory under totems/, run:
//
//	go generate ./totems/
//
// to regenerate the import list.
//
//go:generate go run ../internal/tools/genimports -dir . -out imports_gen.go -pkg totems
package totems

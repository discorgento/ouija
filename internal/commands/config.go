package commands

import "fmt"

// Config edits the global config. It delegates to Setup (same wizard).
func Config(args []string) error {
	fmt.Println("(editing global config — same as `ouija setup`)")
	return Setup(args)
}

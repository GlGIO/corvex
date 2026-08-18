package cmd

import (
	"fmt"
	"strings"

	"github.com/giovannialves/corvex/internal/ops"
)

// renderRecipeDetail prints a recipe the way F2 taught it to be read: the work
// on one axis (kind) and the decisions on another (gates), never mixed into one
// column.
func renderRecipeDetail(r ops.RecipeDetail) {
	fmt.Printf("%s  ·  %d stage(s), %d gate(s)", r.Name, r.Stages, r.Gates)
	if r.Compiled {
		fmt.Print("  ·  compiled")
	}
	fmt.Println()
	if r.Description != "" {
		fmt.Printf("%s\n", r.Description)
	}
	fmt.Printf("%s\n\n", r.Path)

	for _, s := range r.StageList {
		fmt.Printf("  %-10s %-6s %s\n", s.ID, s.Kind, s.Title)
		if len(s.DependsOn) > 0 {
			fmt.Printf("             after %s\n", strings.Join(s.DependsOn, ", "))
		}
		if s.Command != "" {
			fmt.Printf("             $ %s\n", s.Command)
		}
		if s.FanOut {
			fmt.Println("             fan-out over discovered items")
		}
		for _, g := range s.Gates {
			fmt.Printf("             gate %s\n", g)
		}
		if len(s.Evidence) > 0 {
			fmt.Printf("             evidence: %s\n", strings.Join(s.Evidence, ", "))
		}
	}
	fmt.Printf("\n  → corvex run start %s\n", r.Name)
}

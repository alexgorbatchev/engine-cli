package main

import "github.com/alexgorbatchev/engine-cli/internal/libraryops"

func displayLibraryPlan(plan *libraryops.Plan) *libraryops.Plan {
	display := *plan
	display.Path = displayPath(plan.Path)
	display.Destination = displayPath(plan.Destination)
	display.Files = append([]libraryops.File{}, plan.Files...)
	for i := range display.Files {
		display.Files[i].Path = displayPath(display.Files[i].Path)
		display.Files[i].Destination = displayPath(display.Files[i].Destination)
		display.Files[i].Libraries = displayLibraryReferences(display.Files[i].Libraries)
	}
	display.AffectedLibraries = displayLibraryReferences(plan.AffectedLibraries)
	display.UnavailableLibraries = displayLibraryReferences(plan.UnavailableLibraries)
	return &display
}

func displayLibraryReferences(libraries []libraryops.Library) []libraryops.Library {
	display := append([]libraryops.Library{}, libraries...)
	for i := range display {
		display[i].Path = displayPath(display[i].Path)
	}
	return display
}

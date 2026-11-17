//go:build testdesktop

package main

import "github.com/alexgorbatchev/engine-cli/internal/testdesktop"

// Tests that run a built CLI as a subprocess build it with the testdesktop tag,
// so its mutation guard observes a closed Engine DJ Desktop instead of the
// machine's processes. Release and development builds never set the tag.
func init() { querySystem = testdesktop.Closed }
